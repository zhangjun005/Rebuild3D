/**
 * Go 推理服务的客户端：`GET /api/health` + `POST /api/infer`。
 *
 * 约定（见 docs/selection.md §4.4 / §4.2）：
 *   POST /api/infer  multipart/form-data
 *     image        插画文件（png / jpeg / gif；webp 不支持——Go 标准库限制）
 *     focal_factor 默认 1.0（艺术参数，fx = focal_factor · max(W,H)）
 *     max_points   默认 200000（体素下采样目标）
 *     voxel_size   默认 0 = 自动
 *     edge_thresh  默认 0 = 自动（相邻深度差 p99）
 *   响应：application/octet-stream，body 就是 ILC1（和 /__testdata/sample.ilcl 同格式）
 *         响应头 X-ILC1-Pointcount / X-Depth-Width / X-Depth-Height / X-Infer-Ms /
 *                X-Geometry-Ms / X-Total-Ms
 *
 * 开发/预览期由 vite proxy 把 /api/* 转到 http://127.0.0.1:18123，所以这里用相对路径即可
 * （不用管 CORS）。
 */

import { parseIlcl, type IlcCloud } from './ilcl';

export interface SidecarStatus {
  ready: boolean;
  warming: boolean;
  model?: string;
  device?: string;
  process_res?: number;
  vram_used_mb?: number;
  vram_peak_mb?: number;
  load_seconds?: number;
  last_infer_seconds?: number;
  pid?: number;
  restarts?: number;
  error?: string;
}

export interface HealthResponse {
  status: string;
  version?: string;
  pointCount?: number;
  sidecar?: SidecarStatus;
  fixtureError?: string;
  staticError?: string;
}

/** 查服务状态；服务没起来时返回 null（调用方自己决定怎么提示） */
export async function fetchHealth(signal?: AbortSignal): Promise<HealthResponse | null> {
  try {
    const res = await fetch('/api/health', { signal, cache: 'no-store' });
    if (!res.ok) return null;
    return (await res.json()) as HealthResponse;
  } catch {
    return null;
  }
}

export interface InferOptions {
  /** 艺术参数：fx = fy = focalFactor · max(W,H)。越大透视越强。默认 1.0（用服务端默认） */
  focalFactor?: number;
  /** 体素下采样目标上限（默认 200000） */
  maxPoints?: number;
  /** 体素边长；0 = 自动 */
  voxelSize?: number;
  /** 边缘阈值；0 = 自动（p99 深度差） */
  edgeThresh?: number;
  signal?: AbortSignal;
  /** 阶段回调（给 UI 显示"模型加载中/推理中"） */
  onPhase?: (phase: string) => void;
}

export interface InferMetrics {
  pointCount: number;
  depthWidth: number;
  depthHeight: number;
  inferMs: number;
  geometryMs: number;
  totalMs: number;
}

export interface InferResult {
  cloud: IlcCloud;
  metrics: InferMetrics;
  /** 服务端返回的 ILC1 原始字节（已经零拷贝挂在 cloud 上） */
  buffer: ArrayBuffer;
}

function headerNumber(headers: Headers, name: string): number {
  const raw = headers.get(name);
  const value = raw === null ? NaN : Number.parseFloat(raw);
  return Number.isFinite(value) ? value : 0;
}

/** 把插画送到 Go 侧推理，拿回 ILC1 点云 */
export async function inferIllustration(file: File, options: InferOptions = {}): Promise<InferResult> {
  const form = new FormData();
  form.append('image', file, file.name);
  if (options.focalFactor !== undefined) form.append('focal_factor', String(options.focalFactor));
  if (options.maxPoints !== undefined) form.append('max_points', String(options.maxPoints));
  if (options.voxelSize !== undefined) form.append('voxel_size', String(options.voxelSize));
  if (options.edgeThresh !== undefined) form.append('edge_thresh', String(options.edgeThresh));

  options.onPhase?.('推理中（首帧可能要等模型预热）');
  const response = await fetch('/api/infer', {
    method: 'POST',
    body: form,
    signal: options.signal,
  });

  if (!response.ok) {
    // 服务端错误是 JSON：{error, hint?}
    let message = `HTTP ${response.status}`;
    try {
      const payload = (await response.json()) as { error?: string; hint?: string };
      if (payload.error) message = payload.hint ? `${payload.error}（${payload.hint}）` : payload.error;
    } catch {
      /* 不是 JSON 就用状态码 */
    }
    throw new Error(message);
  }

  const buffer = await response.arrayBuffer();
  const cloud = parseIlcl(buffer);
  const metrics: InferMetrics = {
    pointCount: headerNumber(response.headers, 'X-ILC1-PointCount') || cloud.count,
    depthWidth: headerNumber(response.headers, 'X-Depth-Width'),
    depthHeight: headerNumber(response.headers, 'X-Depth-Height'),
    inferMs: headerNumber(response.headers, 'X-Infer-Ms'),
    geometryMs: headerNumber(response.headers, 'X-Geometry-Ms'),
    totalMs: headerNumber(response.headers, 'X-Total-Ms'),
  };
  return { cloud, metrics, buffer };
}

/**
 * 只取元数据（不下载 ILC1 字节）：`POST /api/infer?format=meta`。调试/参数试验用。
 * 注意：这也会真的跑一次推理（sidecar 有成本）。
 */
export async function inferMetaOnly(file: File, options: InferOptions = {}): Promise<Record<string, unknown>> {
  const form = new FormData();
  form.append('image', file, file.name);
  const response = await fetch('/api/infer?format=meta', { method: 'POST', body: form, signal: options.signal });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  return (await response.json()) as Record<string, unknown>;
}

/** 轮询等 sidecar 就绪（模型加载实测 7~12s，冷启动可能更久；上限 180s） */
export async function waitForSidecar(
  options: { timeoutSeconds?: number; onTick?: (status: SidecarStatus | undefined, elapsedSeconds: number) => void; signal?: AbortSignal } = {},
): Promise<SidecarStatus> {
  const timeout = (options.timeoutSeconds ?? 180) * 1000;
  const startedAt = performance.now();
  let last: SidecarStatus | undefined;

  for (;;) {
    const health = await fetchHealth(options.signal);
    last = health?.sidecar;
    const elapsed = (performance.now() - startedAt) / 1000;
    options.onTick?.(last, elapsed);

    if (last?.ready) return last;
    if (last?.error) throw new Error(`sidecar 预热失败：${last.error}`);
    if ((health && !last) || (last && !last.warming && !last.ready)) {
      // 服务在但没启用 sidecar（或状态异常）—— 等下去也没意义
      throw new Error('Go 服务没有启用 sidecar（用 -sidecar-python 指定 illust3d 环境的 python.exe）');
    }
    if (performance.now() - startedAt > timeout) {
      throw new Error(`等待 sidecar 就绪超时（${Math.round(timeout / 1000)}s）`);
    }
    await new Promise((resolve) => window.setTimeout(resolve, 800));
  }
}
