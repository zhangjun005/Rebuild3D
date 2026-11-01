/**
 * 循环录制 / 帧序列出帧（A 阶段）。
 *
 * 目标：**t 与 t+T 的画面必须逐位相同**，否则循环会跳帧。
 *
 * 做法（关键）：捕获时不走真实时间。`Viewer.beginCapture()` 把一切随时间变化的状态
 * 都变成「帧索引 i」的纯函数：
 *
 *   N   = seconds × 30            （固定 30fps 输出，不跟显示器 144Hz 走）
 *   方位角  azimuth(i) = azimuth0 + 2π · turns · (i mod N) / N
 *   沸腾相位 phase(i)  = floor((i mod N) / framesPerPhase)  mod  phases
 *                        framesPerPhase = 30 / boilHz（按能整除 N 取整）
 *
 * 因为先取模再算，i = N 与 i = 0 逐位相同；而 i 从 N-1 到 0 的一步正好是
 * 2π·turns/N 的角度增量（旋转连续）、相位也按同一节奏重掷（沸腾连续）。
 * 所以「N 帧、首尾相接」构成一个无缝循环：播放时显示第 0..N-1 帧，回到第 0 帧
 * 等价于播放第 N 帧，不需要额外补帧。
 *
 * 尺寸：录制时 pixelRatio 固定 1，绘图缓冲 = 目标像素（1920×1080 / 1080×1080 / 视口）。
 */

import { CAPTURE_FPS, type CapturePlan, type Viewer } from './viewer';
import type { PresetKey, RenderParams } from './params';

export interface OutputSize {
  width: number;
  height: number;
}

export type OutputSizeKey = 'viewport' | '1080p' | 'square';

/** 分辨率选项：1080×1080 是社交平台最常用的方图 */
export function resolveOutputSize(key: OutputSizeKey): OutputSize {
  switch (key) {
    case '1080p':
      return { width: 1920, height: 1080 };
    case 'square':
      return { width: 1080, height: 1080 };
    case 'viewport':
    default: {
      // 编码器对奇数尺寸不友好，向下取偶数
      const width = Math.max(2, Math.floor(window.innerWidth / 2) * 2);
      const height = Math.max(2, Math.floor(window.innerHeight / 2) * 2);
      return { width, height };
    }
  }
}

/** 1.30 → "1.3"，2.00 → "2"，0.90 → "0.9" */
function fmt(value: number): string {
  return value
    .toFixed(2)
    .replace(/0+$/, '')
    .replace(/\.$/, '');
}

/**
 * 导出文件名：预设名 + 关键参数 + 尺寸 + 时长，方便同一风格多组参数对比。
 * 例：stipple-print_g1.3_d0.9_p2.5_e1_1080x1080_6s.webm
 */
export function buildCaptureName(
  preset: PresetKey,
  params: RenderParams,
  size: OutputSize,
  seconds: number,
  ext: string,
): string {
  const parts = [
    preset,
    `g${fmt(params.densityGamma)}`,
    `d${fmt(params.stipple)}`,
    `p${fmt(params.pointSize)}`,
    `e${fmt(params.edgeStrength)}`,
    `${size.width}x${size.height}`,
    `${fmt(seconds)}s`,
  ];
  return `${parts.join('_')}.${ext}`;
}

// ── 小工具 ──────────────────────────────────────────────────────────────────

function nextFrame(): Promise<void> {
  return new Promise((resolve) => requestAnimationFrame(() => resolve()));
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function canvasToPng(canvas: HTMLCanvasElement): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (blob) resolve(blob);
      else reject(new Error('canvas.toBlob 失败'));
    }, 'image/png');
  });
}

async function sha256(blob: Blob): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', await blob.arrayBuffer());
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

/** 触发浏览器下载 */
export function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = filename;
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
}

/** 选择浏览器支持的 WebM 编码（VP9 优先） */
function pickWebMMimeType(): string {
  if (typeof MediaRecorder === 'undefined') return '';
  const candidates = ['video/webm;codecs=vp9', 'video/webm;codecs=vp8', 'video/webm'];
  for (const type of candidates) {
    if (MediaRecorder.isTypeSupported(type)) return type;
  }
  return '';
}

export function isRecordingSupported(): boolean {
  return typeof HTMLCanvasElement !== 'undefined' && 'captureStream' in HTMLCanvasElement.prototype && pickWebMMimeType() !== '';
}

// ── WebM 录制 ───────────────────────────────────────────────────────────────

export interface RecordOptions {
  seconds: number;
  size: OutputSize;
  turns?: number;
  /** 码率（bps），默认 12 Mbps */
  bitrate?: number;
  onProgress?: (done: number, total: number) => void;
}

export interface RecordResult {
  blob: Blob;
  plan: CapturePlan;
  mimeType: string;
  size: OutputSize;
}

/**
 * 用 MediaRecorder 录一段**正好一个循环**的 WebM。
 * 帧内容由帧索引决定（见文件头），所以录出来的第一段就是可循环的。
 */
export async function recordWebM(viewer: Viewer, options: RecordOptions): Promise<RecordResult> {
  if (viewer.isCapturing) throw new Error('正在录制/出帧，请稍候');
  const mimeType = pickWebMMimeType();
  if (!mimeType) throw new Error('浏览器不支持 MediaRecorder 录 WebM');

  const canvas = viewer.canvas;
  viewer.setOutputSize(options.size);
  const plan = viewer.beginCapture({
    totalFrames: Math.round(options.seconds * CAPTURE_FPS),
    turns: options.turns ?? 1,
  });

  try {
    // 预热：先画一帧并等两个合成周期，确保首帧不会有上一状态的残留
    viewer.renderCaptureFrame(0);
    await nextFrame();
    await nextFrame();

    // captureStream(0) = 不自动采样，由 requestFrame() 精确控制「每输出帧一次」
    const stream = canvas.captureStream(0);
    const track = stream.getVideoTracks()[0] as CanvasCaptureMediaStreamTrack | undefined;
    if (!track) throw new Error('canvas.captureStream 没有返回视频轨');

    const recorder = new MediaRecorder(stream, {
      mimeType,
      videoBitsPerSecond: options.bitrate ?? 12_000_000,
    });
    const chunks: BlobPart[] = [];
    recorder.addEventListener('dataavailable', (event) => {
      if (event.data.size > 0) chunks.push(event.data);
    });
    const stopped = new Promise<void>((resolve) => {
      recorder.addEventListener('stop', () => resolve(), { once: true });
    });

    recorder.start();
    // 第 0 帧
    viewer.renderCaptureFrame(0);
    track.requestFrame();
    options.onProgress?.(1, plan.totalFrames);

    // 按 30fps 的时间片推进；渲染仍在 rAF 里做，但内容只由帧索引决定
    await new Promise<void>((resolve) => {
      const start = performance.now();
      let index = 1;
      const step = (): void => {
        const elapsed = performance.now() - start;
        while (index < plan.totalFrames && elapsed >= (index * 1000) / CAPTURE_FPS - 1) {
          viewer.renderCaptureFrame(index);
          track.requestFrame();
          index += 1;
          options.onProgress?.(index, plan.totalFrames);
        }
        if (index < plan.totalFrames) requestAnimationFrame(step);
        else resolve();
      };
      requestAnimationFrame(step);
    });

    // 多等一个帧周期再停，避免最后一帧被编码器截掉
    await sleep((1000 / CAPTURE_FPS) * 1.5);
    recorder.stop();
    await stopped;
    track.stop();

    return { blob: new Blob(chunks, { type: mimeType }), plan, mimeType, size: options.size };
  } finally {
    viewer.endCapture();
    viewer.setOutputSize(null);
  }
}

// ── 帧序列（给 Go 侧 image/gif 编码用）─────────────────────────────────────

export interface FramesOptions {
  seconds: number;
  /** 不给就沿用当前视口尺寸 */
  size?: OutputSize;
  turns?: number;
  /** 额外返回 index == totalFrames 的「回绕帧」，用于自检 */
  includeWrapFrame?: boolean;
  onProgress?: (done: number, total: number) => void;
}

export interface FramesResult {
  frames: Blob[];
  plan: CapturePlan;
  size: OutputSize;
  wrapFrame?: Blob;
}

/**
 * 逐帧导出 PNG。前端只出帧，GIF 由 Go 的 image/gif 编码（见任务约定）。
 *
 * 注意内存：PNG 每帧约 1~2 MB，180 帧就是 200~300 MB。
 * 真正接 Go 侧时建议分块（例如每 30 帧 POST 一次）而不是一次性拿全量。
 */
export async function captureFrames(viewer: Viewer, options: FramesOptions): Promise<FramesResult> {
  if (viewer.isCapturing) throw new Error('正在录制/出帧，请稍候');

  const size = options.size ?? viewer.captureSize();
  const resized = options.size !== undefined;
  if (resized) viewer.setOutputSize(options.size ?? null);
  const plan = viewer.beginCapture({
    totalFrames: Math.round(options.seconds * CAPTURE_FPS),
    turns: options.turns ?? 1,
  });

  try {
    const frames: Blob[] = [];
    for (let index = 0; index < plan.totalFrames; index++) {
      viewer.renderCaptureFrame(index);
      frames.push(await canvasToPng(viewer.canvas));
      options.onProgress?.(index + 1, plan.totalFrames);
      // 让出主线程，别把 UI 卡死（每 8 帧一次）
      if ((index & 7) === 7) await nextFrame();
    }

    let wrapFrame: Blob | undefined;
    if (options.includeWrapFrame) {
      viewer.renderCaptureFrame(plan.totalFrames);
      wrapFrame = await canvasToPng(viewer.canvas);
    }

    return { frames, plan, size, wrapFrame };
  } finally {
    viewer.endCapture();
    if (resized) viewer.setOutputSize(null);
  }
}

// ── 无缝自检 ────────────────────────────────────────────────────────────────

export interface SeamReport {
  /** 回绕帧（index = N）与首帧（index = 0）是否逐像素相同 */
  identical: boolean;
  hashFirst: string;
  hashWrap: string;
  plan: CapturePlan;
  size: OutputSize;
}

/**
 * 无缝自检：渲染 index 0 与 index N，比较 PNG 的 SHA-256。
 * 相同 ⇒ t 与 t+T 是同一幅画面（循环点不跳）。
 */
export async function verifySeam(
  viewer: Viewer,
  options: { seconds: number; size?: OutputSize; turns?: number },
): Promise<SeamReport> {
  if (viewer.isCapturing) throw new Error('正在录制/出帧，请稍候');
  const resized = options.size !== undefined;
  if (resized) viewer.setOutputSize(options.size ?? null);
  const plan = viewer.beginCapture({
    totalFrames: Math.round(options.seconds * CAPTURE_FPS),
    turns: options.turns ?? 1,
  });

  try {
    viewer.renderCaptureFrame(0);
    const first = await canvasToPng(viewer.canvas);
    viewer.renderCaptureFrame(plan.totalFrames);
    const wrap = await canvasToPng(viewer.canvas);
    const [hashFirst, hashWrap] = await Promise.all([sha256(first), sha256(wrap)]);
    return {
      identical: hashFirst === hashWrap,
      hashFirst,
      hashWrap,
      plan,
      size: options.size ?? viewer.captureSize(),
    };
  } finally {
    viewer.endCapture();
    if (resized) viewer.setOutputSize(null);
  }
}

/**
 * 接缝拼版图：把循环末尾 3 帧和开头 3 帧拼成一张图，
 * 肉眼看第 3→4 格是否连续（不连续的话这里会有明显的跳变）。
 */
export async function captureSeamStrip(
  viewer: Viewer,
  options: { seconds: number; size?: OutputSize; turns?: number; tileWidth?: number },
): Promise<{ blob: Blob; plan: CapturePlan }> {
  if (viewer.isCapturing) throw new Error('正在录制/出帧，请稍候');
  const resized = options.size !== undefined;
  if (resized) viewer.setOutputSize(options.size ?? null);
  const plan = viewer.beginCapture({
    totalFrames: Math.round(options.seconds * CAPTURE_FPS),
    turns: options.turns ?? 1,
  });

  try {
    const tileWidth = options.tileWidth ?? 320;
    const { width, height } = viewer.captureSize();
    const tileHeight = Math.round((height / width) * tileWidth);
    const indices = [-3, -2, -1, 0, 1, 2].map((offset) => (offset + plan.totalFrames) % plan.totalFrames);

    const strip = document.createElement('canvas');
    strip.width = tileWidth * indices.length;
    strip.height = tileHeight;
    const context = strip.getContext('2d');
    if (!context) throw new Error('拿不到 2D context');
    context.fillStyle = '#000';
    context.fillRect(0, 0, strip.width, strip.height);

    for (let slot = 0; slot < indices.length; slot++) {
      const index = indices[slot] as number;
      viewer.renderCaptureFrame(index);
      context.drawImage(viewer.canvas, slot * tileWidth, 0, tileWidth, tileHeight);
    }

    const blob = await new Promise<Blob>((resolve, reject) => {
      strip.toBlob((value) => (value ? resolve(value) : reject(new Error('toBlob 失败'))), 'image/png');
    });
    return { blob, plan };
  } finally {
    viewer.endCapture();
    if (resized) viewer.setOutputSize(null);
  }
}

// ── 录出来的 WebM 自检（元信息 + 帧数）─────────────────────────────────────

export interface VideoProbe {
  width: number;
  height: number;
  /** MediaRecorder 的 WebM 常报 Infinity，这里尽力而为 */
  duration: number;
  /** 播放一遍统计到的呈现帧数（requestVideoFrameCallback；主线程忙时会漏，只当下限） */
  presentedFrames: number;
  /** 媒体管线统计的「已解码总帧数」（getVideoPlaybackQuality，更接近真实帧数） */
  totalVideoFrames: number;
  droppedVideoFrames: number;
}

export async function probeWebM(blob: Blob, timeoutSeconds: number): Promise<VideoProbe> {
  const url = URL.createObjectURL(blob);
  const video = document.createElement('video');
  video.src = url;
  video.muted = true;
  video.playsInline = true;
  video.loop = false;

  const meta = await new Promise<{ width: number; height: number; duration: number }>((resolve) => {
    let settled = false;
    const done = (): void => {
      if (settled) return;
      settled = true;
      resolve({
        width: video.videoWidth,
        height: video.videoHeight,
        duration: Number.isFinite(video.duration) ? video.duration : NaN,
      });
    };
    video.addEventListener('loadedmetadata', done, { once: true });
    video.addEventListener('error', done, { once: true });
    window.setTimeout(done, 3000);
  });

  let presentedFrames = 0;
  const hasRvfc = 'requestVideoFrameCallback' in video;
  if (hasRvfc) {
    const deadline = performance.now() + timeoutSeconds * 1000 + 1500;
    await new Promise<void>((resolve) => {
      const onFrame = (): boolean => {
        presentedFrames += 1;
        if (performance.now() > deadline) return false;
        (video as HTMLVideoElement).requestVideoFrameCallback(onFrame);
        return true;
      };
      (video as HTMLVideoElement).requestVideoFrameCallback(onFrame);
      video.play().catch(() => resolve());
      video.addEventListener('ended', () => resolve(), { once: true });
      window.setTimeout(() => resolve(), timeoutSeconds * 1000 + 1500);
    });
  }

  const quality =
    typeof video.getVideoPlaybackQuality === 'function' ? video.getVideoPlaybackQuality() : null;

  video.pause();
  video.removeAttribute('src');
  video.load();
  URL.revokeObjectURL(url);
  return {
    ...meta,
    presentedFrames,
    totalVideoFrames: quality?.totalVideoFrames ?? -1,
    droppedVideoFrames: quality?.droppedVideoFrames ?? -1,
  };
}
