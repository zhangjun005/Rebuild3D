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
import {
  buildCaptureName,
  captureFrames,
  captureSeamStrip,
  downloadBlob,
  isRecordingSupported,
  probeWebM,
  recordWebM,
  resolveOutputSize,
  verifySeam,
} from './capture';
import { fetchHealth, inferIllustration, waitForSidecar } from './infer';
import { loadSampleCloud, parseIlcl } from './ilcl';
import { Panel, type ExportRequest } from './panel';
import { DEFAULT_PRESET } from './params';
import { CAPTURE_FPS, configureBlueNoiseTexture, Viewer } from './viewer';

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
    onChange: () => viewer.setParams(viewer.params),
    onPreset: (key) => viewer.applyPreset(key),
    onExport: (request) => {
      void runExport(request);
    },
    onUpload: (file) => {
      void handleUpload(file);
    },
    onLoadSample: () => {
      void reloadSample();
    },
  });
  viewer.onStats = (stats) => panel.updateStats(stats);
  // 拖拽会自动关掉摆动（viewer 内部改 params），面板要跟着更新勾选框
  viewer.onInternalParamChange = () => panel.sync();

  /** 导出流程（录制 / 出帧 / 无缝自检） */
  const runExport = async (request: ExportRequest): Promise<void> => {
    if (viewer.isCapturing) return;
    const size = resolveOutputSize(request.sizeKey);
    const totalFrames = Math.round(request.seconds * CAPTURE_FPS);
    const sizeLabel = `${size.width}×${size.height}`;
    panel.setExportBusy(true);
    try {
      if (request.action === 'record') {
        if (!isRecordingSupported()) {
          throw new Error('这个浏览器不支持 canvas.captureStream + MediaRecorder');
        }
        panel.setExportStatus(`录制中 0 / ${totalFrames} 帧 · ${sizeLabel}`);
        const result = await recordWebM(viewer, {
          seconds: request.seconds,
          size,
          onProgress: (done, total) => panel.setExportStatus(`录制中 ${done} / ${total} 帧`),
        });
        const name = buildCaptureName(
          panel.currentPreset,
          viewer.params,
          result.size,
          result.plan.seconds,
          'webm',
        );
        downloadBlob(result.blob, name);
        // 回读校验：读帧数/尺寸时先停掉渲染循环，避免主线程抢时间导致统计失真
        viewer.stop();
        const probe = await probeWebM(result.blob, result.plan.seconds);
        viewer.start();
        panel.setExportStatus(
          [
            `已录制 ${name}`,
            `${(result.blob.size / 1048576).toFixed(2)} MB · ${result.mimeType}`,
            `计划 ${result.plan.totalFrames} 帧 / ${result.plan.seconds.toFixed(1)}s / ` +
              `\$\{result.plan.swings\} \u4e2a\u6765\u56de / \u6446\u5e45 \u00b1\$\{result.plan.swayAmplitudeDeg}\u00b0 / 沸腾 ${result.plan.boilHz.toFixed(1)}Hz` +
              `（${result.plan.framesPerPhase} 帧一相 × ${result.plan.phases} 相）`,
            `回读 ${probe.width}×${probe.height} · 时长 ${probe.duration.toFixed(3)}s` +
              `（理论 ${((result.plan.totalFrames - 1) / CAPTURE_FPS).toFixed(3)}s` +
              ` ⇒ ${result.plan.totalFrames} 帧，无首尾重复帧）`,
          ].join('\n'),
        );
      } else if (request.action === 'frames') {
        panel.setExportStatus(`出帧中 0 / ${totalFrames} 帧 · ${sizeLabel}`);
        const result = await captureFrames(viewer, {
          seconds: request.seconds,
          size,
          includeWrapFrame: true,
          onProgress: (done, total) => {
            if (done % 10 === 0 || done === total) panel.setExportStatus(`出帧中 ${done} / ${total} 帧`);
          },
        });
        const bytes = result.frames.reduce((sum, blob) => sum + blob.size, 0);
        const wrapBytes = result.wrapFrame?.size ?? 0;
        const first = result.frames[0]?.size ?? 0;
        panel.setExportStatus(
          [
            `帧序列就绪：${result.frames.length} 帧 PNG · 共 ${(bytes / 1048576).toFixed(1)} MB`,
            `单帧 ${(first / 1024).toFixed(0)} KB · 回绕帧 ${(wrapBytes / 1024).toFixed(0)} KB`,
            `（GIF 由 Go 侧 image/gif 编码；帧已挂在 window.illust3d.captureFrames）`,
          ].join('\n'),
        );
      } else {
        panel.setExportStatus(`无缝自检中 · ${sizeLabel}`);
        const report = await verifySeam(viewer, { seconds: request.seconds, size });
        const strip = await captureSeamStrip(viewer, { seconds: request.seconds, size });
        const name = buildCaptureName(
          panel.currentPreset,
          viewer.params,
          report.size,
          report.plan.seconds,
          'webm',
        ).replace(/\.webm$/, '_seam.png');
        downloadBlob(strip.blob, name);
        panel.setExportStatus(
          [
            report.identical
              ? '无缝自检通过：index 0 与 index N 逐像素相同'
              : '无缝自检失败：index 0 与 index N 不一致',
            `0:${report.hashFirst.slice(0, 12)}… N:${report.hashWrap.slice(0, 12)}…`,
            `接缝拼版图已下载 ${name}`,
          ].join('\n'),
        );
      }
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

  // ── 真实插画 → POST /api/infer → ILC1 → 渲染 ──────────────────────────────
  let busy = false;
  async function handleUpload(file: File): Promise<void> {
    if (busy) return;
    busy = true;
    panel.setInputBusy(true);
    try {
      panel.setInputStatus(`读取 ${file.name}（${(file.size / 1048576).toFixed(1)} MB）…`);
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

      panel.setInputStatus('推理中…（深度 → 点云）');
      const result = await inferIllustration(file, {
        onPhase: (phase) => panel.setInputStatus(phase),
      });
      applyCloud(result.cloud, `${file.name} · ${result.metrics.pointCount.toLocaleString('en-US')} 点`);
      const m = result.metrics;
      panel.setInputStatus(
        [
          `完成：${m.pointCount.toLocaleString('en-US')} 点 · 深度 ${m.depthWidth}×${m.depthHeight}`,
          `推理 ${m.inferMs.toFixed(0)}ms · 几何 ${m.geometryMs.toFixed(0)}ms · 合计 ${m.totalMs.toFixed(0)}ms`,
          `ILC1 ${(result.buffer.byteLength / 1048576).toFixed(2)} MB`,
        ].join('\n'),
      );
      console.info('[illust3d] 推理完成', m);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      console.error('[illust3d] 插画推理失败', error);
      panel.setInputStatus(`失败：${message}`);
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
  dropOverlay.textContent = '松手上传插画 → 深度 → 点云';
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
    if (file) void handleUpload(file);
  });

  window.illust3d = {
    viewer,
    stats: () => viewer.stats(),
    syncPanel: () => panel.sync(),
    loadILC1,
    // 帧序列接口（A 阶段约定）：给 Go 侧 image/gif 编码用
    captureFrames: async (count, options) => {
      const result = await captureFrames(viewer, {
        seconds: count / CAPTURE_FPS,
        ...options,
      });
      return result.frames;
    },
    recordWebM: (seconds, sizeKey = 'square') =>
      recordWebM(viewer, { seconds, size: resolveOutputSize(sizeKey) }),
    verifySeam: (seconds, sizeKey = 'square') =>
      verifySeam(viewer, { seconds, size: resolveOutputSize(sizeKey) }),
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
