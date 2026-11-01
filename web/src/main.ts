/**
 * illust3d 渲染层入口。
 *
 * 数据流：testdata/sample.ilcl（ILC1）→ THREE.BufferGeometry（零拷贝）
 *        → 四 pass 点画印刷管线（viewer.ts）→ 屏幕。
 *
 * 生产环境的点云来自 Go 的 `GET /api/cloud/{id}`（同样的 ILC1 字节流），
 * 开发期由 vite 中间件从 testdata/ 只读转发（见 vite.config.ts）。
 */

import * as THREE from 'three';

import noiseUrl from './assets/blue-noise.png?url';
import { fetchHealth, inferIllustration, waitForSidecar } from './infer';
import { loadSampleCloud, parseIlcl } from './ilcl';
import { Panel, type ExportRequest, type PngSizeKey } from './panel';
import { DEFAULT_PRESET } from './params';
import { configureBlueNoiseTexture, Viewer } from './viewer';

function requireElement<T extends HTMLElement>(id: string): T {
  const element = document.getElementById(id);
  if (!element) throw new Error(`缺少 DOM 节点 #${id}`);
  return element as T;
}

function showFatal(message: string): void {
  const overlay = document.createElement('pre');
  overlay.style.cssText = [
    'position:fixed',
    'inset:24px',
    'margin:0',
    'padding:16px 18px',
    'border-radius:10px',
    'background:rgba(30,12,12,0.92)',
    'color:#ffb4a8',
    'font:12px/1.6 ui-monospace,Consolas,monospace',
    'white-space:pre-wrap',
    'overflow:auto',
    'z-index:99',
  ].join(';');
  overlay.textContent = `illust3d 启动失败\n\n${message}`;
  document.body.append(overlay);
}

/** 导出尺寸（null = 当前视口） */
function resolvePngSize(key: PngSizeKey): { width: number; height: number } | null {
  if (key === 'square') return { width: 1080, height: 1080 };
  if (key === '1080p') return { width: 1920, height: 1080 };
  return null;
}

/** 参数 → 文件名里的短数字（去掉多余的 0） */
function fmtNum(value: number): string {
  return String(Math.round(value * 100) / 100);
}

/** PNG 文件名：预设 + 关键参数 + 尺寸（录制子系统删掉后不再有时长） */
function buildPngName(
  preset: string,
  params: { densityGamma: number; stipple: number; pointSize: number; edgeStrength: number; parallaxMode: boolean; parallaxAmount: number; swayAmplitude: number },
  size: { width: number; height: number } | null,
): string {
  const dims = size
    ? `${size.width}x${size.height}`
    : `${window.innerWidth}x${window.innerHeight}`;
  return (
    [
      preset,
      `g${fmtNum(params.densityGamma)}`,
      `d${fmtNum(params.stipple)}`,
      `p${fmtNum(params.pointSize)}`,
      `e${fmtNum(params.edgeStrength)}`,
      params.parallaxMode
        ? `par${fmtNum(params.parallaxAmount)}`
        : `sw${Math.round(params.swayAmplitude)}`,
      dims,
    ].join('_') + '.png'
  );
}

function downloadBlob(blob: Blob, name: string): void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = name;
  document.body.append(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 2000);
}

async function boot(): Promise<void> {
  const canvas = requireElement<HTMLCanvasElement>('view');
  const panelElement = requireElement<HTMLElement>('panel');
  const hudElement = requireElement<HTMLElement>('hud');
  hudElement.textContent = 'FPS -- · 加载中…';

  // 1) blue noise 纹理：点画阈值（L2）+ 纸颗粒（L4）共用
  const noise = configureBlueNoiseTexture(
    await new THREE.TextureLoader().loadAsync(noiseUrl),
  );

  // 2) Viewer（渲染管线）
  const viewer = new Viewer(canvas, noise);

  // URL 开关：?baseView=threeQuarter / ?baseView=3q 用 3/4 基准视角（看 fixture 的立体感）
  // 默认是**正面**基准：深度图反投影的点云 +Z 朝观察者，作品本来的样子就是正面。
  const query = new URLSearchParams(window.location.search);
  const baseView = query.get('baseView');
  if (baseView === 'threeQuarter' || baseView === '3q') {
    viewer.params.threeQuarterView = true;
    viewer.setParams(viewer.params);
  }

  // 3) 面板
  const panel = new Panel({
    container: panelElement,
    hud: hudElement,
    params: viewer.params,
    activePreset: DEFAULT_PRESET,
    onChange: () => {
      viewer.setParams(viewer.params);
      // 模式切换：状态栏跟着变（不能出现"图像模式却写着 400,000 点"这种矛盾）
      const mode = viewer.params.renderMode;
      if (mode !== lastMode) {
        lastMode = mode;
        if (mode === 'image') {
          if (lastFile && !viewer.hasImage) void useImageMode(lastFile);
          else {
            panel.setInputStatus(
              [
                `图像模式（对照）：${lastFile?.name ?? ''}`,
                '原图直接风格化：线稿/颜色保留，但没有立体感。',
                '点云模式才是产品路径 —— 点上面「点云」切回。',
              ].join('\n'),
            );
          }
        } else {
          panel.setInputStatus(
            lastCloudMetrics ? cloudReadyStatus() : '点云模式：上传一张插画即可生成点云',
          );
          // 切回点云但这份图还没生成过 → 懒加载
          if (lastFile && cloudForFile !== lastFile) void generateCloud();
        }
      }
    },
    onPreset: (key) => viewer.applyPreset(key),
    onExport: (request) => {
      void runExport(request);
    },
    onUpload: (file) => {
      lastFile = file;
      cloudForFile = null;
      // 产品路径：上传直接走点云（图像模式只是可选/回退，不再是默认）
      void generateCloud();
    },
    onLoadSample: () => {
      void reloadSample();
    },
    onReset: () => {
      viewer.resetView();
      panel.sync();
    },
  });
  viewer.onStats = (stats) => panel.updateStats(stats);
  // 拖拽会自动关掉摆动（viewer 内部改 params），面板要跟着更新勾选框
  viewer.onInternalParamChange = () => panel.sync();

  /**
   * 导出 PNG：把当前画面（含当前运动姿态/沸腾相位）存成图片。
   * 只出图片——不做 GIF/视频（scope 变更后录制子系统已整体删除）。
   */
  const runExport = async (request: ExportRequest): Promise<void> => {
    panel.setExportBusy(true);
    panel.setExportStatus('导出中…');
    try {
      const size = resolvePngSize(request.sizeKey);
      const blob = await viewer.exportPng(size);
      const name = buildPngName(panel.currentPreset, viewer.params, size);
      downloadBlob(blob, name);
      const dims = size ? `${size.width}×${size.height}` : '当前视口';
      panel.setExportStatus(`已导出 PNG · ${dims} · ${(blob.size / 1048576).toFixed(1)} MB`);
      console.info('[illust3d] 已导出 PNG', name, blob.size);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      console.error('[illust3d] 导出失败', error);
      panel.setExportStatus(`导出失败：${message}`);
    } finally {
      panel.setExportBusy(false);
    }
  };
  // 4) 点云
  const { cloud, source } = await loadSampleCloud();
  viewer.setCloud(cloud, source);
  panel.setSource(source);
  panel.sync(); // setCloud 会按包围盒/点数自适应点大小，面板要跟上

  // 5) 跑起来
  viewer.start();

  /** 应用一份点云（自适应取景/点大小/深度区间都在 Viewer.setCloud 里重算） */
  const applyCloud = (next: ReturnType<typeof parseIlcl>, label: string): void => {
    viewer.setCloud(next, label);
    panel.setSource(label);
    panel.sync();
  };

  /**
   * 换一份点云（真实深度点云接进来时走这里；也方便用控制台/自动化喂数据）。
   */
  const loadILC1 = (buffer: ArrayBuffer, label = '内存中的 ILC1'): number => {
    const next = parseIlcl(buffer);
    applyCloud(next, label);
    console.info(`[illust3d] 已切换到 ${label} · ${next.count.toLocaleString('en-US')} 点`);
    return next.count;
  };

  // ── 上传插画：图像模式（浏览器内，不请求 Go）当默认；点云模式按需懒加载 ──
  let busy = false;
  let lastFile: File | null = null;
  let cloudForFile: File | null = null;

  /** 把文件变成纹理 + 平均亮度（判断主体极性"自动"用）。纯浏览器，不碰 Go。 */
  const loadImageTexture = async (
    file: File,
  ): Promise<{ texture: THREE.Texture; aspect: number; meanLuminance: number }> => {
    const bitmap = await createImageBitmap(file);
    const texture = new THREE.Texture(bitmap);
    texture.colorSpace = THREE.NoColorSpace;
    texture.flipY = false; // ImageBitmap 已经是屏幕方向，再翻一次会上下颠倒
    texture.minFilter = THREE.LinearFilter;
    texture.magFilter = THREE.LinearFilter;
    texture.generateMipmaps = false;
    texture.needsUpdate = true;
    // 缩到 64px 算平均亮度（只用于主体极性的"自动"判定）
    const probe = document.createElement('canvas');
    probe.width = 64;
    probe.height = 64;
    const context = probe.getContext('2d');
    let meanLuminance = 0.5;
    if (context) {
      context.drawImage(bitmap, 0, 0, 64, 64);
      const data = context.getImageData(0, 0, 64, 64).data;
      let sum = 0;
      for (let i = 0; i < data.length; i += 4) {
        sum += (0.299 * (data[i] ?? 0) + 0.587 * (data[i + 1] ?? 0) + 0.114 * (data[i + 2] ?? 0)) / 255;
      }
      meanLuminance = sum / (data.length / 4);
    }
    return { texture, aspect: bitmap.width / Math.max(bitmap.height, 1), meanLuminance };
  };

  /**
   * 把图片装进 viewer（不改变渲染模式）。
   * 图像模式是**可选/回退**路径：上传默认走点云，只有用户显式切模式或推理失败时才用到它。
   */
  async function ensureImage(file: File): Promise<void> {
    const { texture, aspect, meanLuminance } = await loadImageTexture(file);
    viewer.setImage(texture, aspect, meanLuminance);
  }

  /** 图像模式（对照/回退）：用户显式切换、或点云失败时用 */
  async function useImageMode(file: File): Promise<void> {
    try {
      panel.setInputStatus(`读取 ${file.name}（${(file.size / 1048576).toFixed(1)} MB）…`);
      await ensureImage(file);
      viewer.params.renderMode = 'image';
      viewer.params.inkSource = 1;
      // 图像模式降级为对照/回退：默认用「彩色点画」（保留原画颜色），别一上来就是重铜版画
      viewer.applyPreset('color-print');
      viewer.params.renderMode = 'image';
      viewer.setParams(viewer.params);
      panel.setPreset('color-print');
      panel.setImageSource(file.name);
      panel.setInputStatus(
        [
          `图像模式（对照/回退）：${file.name}`,
          '图像模式直接用原图做风格化，不经过深度：线稿保留，但没有立体感。',
          '点云模式才是产品路径 —— 切回「点云」即可重新生成。',
        ].join('\n'),
      );
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      console.error('[illust3d] 图像模式载入失败', error);
      panel.setInputStatus(`图像模式载入失败：${message}`);
    }
  }

  // ── 点云模式（产品路径）：POST /api/infer → ILC1 → 渲染 ─────────────────────
  /** 点云模式的采样上限：最终配方 400,000（点云是产品路径，别再给 20 万） */
  const CLOUD_MAX_POINTS = 400000;
  /** 最近一次点云生成结果（模式来回切换时用来重建状态栏） */
  let lastCloudMetrics: { pointCount: number; depthWidth: number; depthHeight: number; inferMs: number; geometryMs: number; totalMs: number; bytes: number } | null = null;
  let lastMode: 'cloud' | 'image' = viewer.params.renderMode;

  /** 点云就绪状态栏（点数是用户判断"生成了没有"的唯一依据） */
  function cloudReadyStatus(): string {
    const m = lastCloudMetrics;
    if (!m) return '✓ 点云就绪';
    return [
      `✓ 点云就绪：${m.pointCount.toLocaleString('en-US')} 点 · 深度 ${m.depthWidth}×${m.depthHeight}`,
      `推理 ${m.inferMs.toFixed(0)}ms · 几何 ${m.geometryMs.toFixed(0)}ms · 合计 ${m.totalMs.toFixed(0)}ms · ILC1 ${(m.bytes / 1048576).toFixed(2)} MB`,
      '左键拖拽旋转 · 滚轮缩放 · 「回正」回到正面',
    ].join('\n');
  }


  async function generateCloud(): Promise<void> {
    const file = lastFile;
    if (!file) {
      panel.setInputStatus('先上传一张插画，再切点云模式');
      return;
    }
    if (busy) return;
    busy = true;
    panel.setInputBusy(true);
    try {
      const health = await fetchHealth();
      if (!health) {
        throw new Error('连不上 Go 服务 /api/health（用 out\\illust3d.exe -web-dist web/dist 起服务）');
      }
      if (!health.sidecar) {
        throw new Error('Go 服务没有启用 sidecar（启动时加 -sidecar-python）');
      }
      if (!health.sidecar.ready) {
        panel.setInputStatus(
          `模型加载中…（${health.sidecar.warming ? 'warming' : '未就绪'}，实测 7~12s）`,
        );
        const status = await waitForSidecar({
          onTick: (s, seconds) => {
            panel.setInputStatus(
              s?.ready
                ? `模型就绪（加载 ${(s.load_seconds ?? 0).toFixed(1)}s）`
                : `模型加载中… 已等 ${seconds.toFixed(0)}s${s?.warming ? '（warming）' : ''}`,
            );
          },
        });
        panel.setInputStatus(`模型就绪（加载 ${(status.load_seconds ?? 0).toFixed(1)}s，显存 ${Math.round(status.vram_used_mb ?? 0)} MiB）`);
      }

      panel.setInputStatus(
        `推理中…（深度 → 点云，最多 ${CLOUD_MAX_POINTS.toLocaleString('en-US')} 点，首次可能要 1~3s）`,
      );
      const result = await inferIllustration(file, {
        maxPoints: CLOUD_MAX_POINTS,
        onPhase: (phase) => panel.setInputStatus(phase),
      });
      panel.setInputStatus(`生成点云…（${result.metrics.pointCount.toLocaleString('en-US')} 点）`);
      applyCloud(result.cloud, `${file.name} · ${result.metrics.pointCount.toLocaleString('en-US')} 点`);
      cloudForFile = file;
      // 产品路径就是点云（用户可能从图像模式切过来）
      viewer.params.renderMode = 'cloud';
      // 近层（主体）上墨、远景（背景）留纸，figure-ground 才分得开；按亮度会让白主体消失。
      if (viewer.params.inkSource !== 2) {
        viewer.params.inkSource = 2;
        viewer.params.inkPolarity = 2;
        viewer.setParams(viewer.params);
        panel.sync();
      }
      const m = result.metrics;
      lastCloudMetrics = { ...m, bytes: result.buffer.byteLength };
      panel.setInputStatus(cloudReadyStatus());
      // 顺带把原图装好：用户想切「图像模式（对照）」时不用再等
      void ensureImage(file)
        .then(() => panel.setImageSource(file.name))
        .catch((error: unknown) => {
          console.warn('[illust3d] 图像对照准备失败（不影响点云）', error);
        });
      console.info('[illust3d] 推理完成', m);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      console.error('[illust3d] 点云生成失败', error);
      panel.setInputStatus(
        `点云生成失败：${message}\n已自动切到「图像模式」——可直接看画面，修好后切回「点云」重试`,
      );
      if (file) await useImageMode(file);
    } finally {
      busy = false;
      panel.setInputBusy(false);
    }
  }

  async function reloadSample(): Promise<void> {
    if (busy) return;
    try {
      panel.setInputStatus('加载样例点云…');
      const loaded = await loadSampleCloud();
      applyCloud(loaded.cloud, loaded.source);
      panel.setInputStatus(`样例点云：${loaded.cloud.count.toLocaleString('en-US')} 点`);
    } catch (error) {
      panel.setInputStatus(`样例点云加载失败：${error instanceof Error ? error.message : String(error)}`);
    }
  }

  // 全窗口拖拽上传
  const dropOverlay = document.createElement('div');
  dropOverlay.className = 'drop-overlay';
  dropOverlay.textContent = '松手上传插画 → 图像模式直接出图（线稿保留）';
  document.body.append(dropOverlay);
  let dragDepth = 0;
  const hasFiles = (event: DragEvent): boolean =>
    Array.from(event.dataTransfer?.types ?? []).includes('Files');
  window.addEventListener('dragenter', (event) => {
    if (!hasFiles(event)) return;
    dragDepth += 1;
    dropOverlay.classList.add('active');
  });
  window.addEventListener('dragleave', () => {
    dragDepth = Math.max(0, dragDepth - 1);
    if (dragDepth === 0) dropOverlay.classList.remove('active');
  });
  window.addEventListener('dragover', (event) => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    event.dataTransfer!.dropEffect = 'copy';
  });
  window.addEventListener('drop', (event) => {
    event.preventDefault();
    dragDepth = 0;
    dropOverlay.classList.remove('active');
    const file = event.dataTransfer?.files?.[0];
    if (file) {
      lastFile = file;
      cloudForFile = null;
      void generateCloud();
    }
  });

  window.illust3d = {
    viewer,
    stats: () => viewer.stats(),
    syncPanel: () => panel.sync(),
    loadILC1,
    /** 导出 PNG（自动化里直接拿 Blob，不用点按钮） */
    exportPng: (size: { width: number; height: number } | null) => viewer.exportPng(size),
  };

  console.info(
    `[illust3d] 点画印刷 ready · ${cloud.count.toLocaleString('en-US')} 点 · ` +
      `${(cloud.byteLength / 1048576).toFixed(2)} MB · 来源 ${source}`,
  );
  console.info('[illust3d] 调试入口：window.illust3d.stats()');
}

void boot().catch((error: unknown) => {
  const message = error instanceof Error ? (error.stack ?? error.message) : String(error);
  console.error('[illust3d] 启动失败', error);
  showFatal(message);
});
