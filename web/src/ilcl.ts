/**
 * ILC1 点云解析 —— docs/selection.md §4.2 的内部传输格式。
 *
 *   magic  "ILC1"        4 B
 *   count  uint32        4 B
 *   pos    float32[3*count]
 *   col    u8[3*count]
 *
 * 全小端。**零拷贝**：位置和颜色都是同一个 ArrayBuffer 上的视图，
 * 直接喂给 THREE.BufferAttribute，不再复制一份（118k 点 ≈ 1.7 MB）。
 */

export interface IlcCloud {
  /** 点数 */
  readonly count: number;
  /** float32 视图（shape = count*3），与 ArrayBuffer 共享内存 */
  readonly positions: Float32Array;
  /** u8 视图（shape = count*3，归一化后进 shader 是 0..1） */
  readonly colors: Uint8Array;
  /** 原始字节数（含 8 字节头） */
  readonly byteLength: number;
  /** 位置包围盒，用于相机取景与自检 */
  readonly bounds: {
    min: [number, number, number];
    max: [number, number, number];
  };
}

const MAGIC = 'ILC1';
const HEADER_BYTES = 8;
const BYTES_PER_POINT = 12 + 3;

/** 解析 ILC1 字节流（不复制数据） */
export function parseIlcl(buffer: ArrayBuffer): IlcCloud {
  if (buffer.byteLength < HEADER_BYTES) {
    throw new Error(`ILC1: 数据太短（${buffer.byteLength} B）`);
  }

  const magic = String.fromCharCode(...new Uint8Array(buffer, 0, 4));
  if (magic !== MAGIC) {
    throw new Error(`ILC1: magic 不匹配（拿到 "${magic}"，期望 "${MAGIC}"）`);
  }

  const count = new DataView(buffer).getUint32(4, true);
  const expected = HEADER_BYTES + count * BYTES_PER_POINT;
  if (buffer.byteLength !== expected) {
    throw new Error(
      `ILC1: 长度不符（count=${count} 应为 ${expected} B，实际 ${buffer.byteLength} B）`,
    );
  }

  // 零拷贝。offset 8 是 4 字节对齐的，Float32Array 视图合法。
  const positions = new Float32Array(buffer, HEADER_BYTES, count * 3);
  const colors = new Uint8Array(buffer, HEADER_BYTES + count * 12, count * 3);

  const min: [number, number, number] = [Infinity, Infinity, Infinity];
  const max: [number, number, number] = [-Infinity, -Infinity, -Infinity];
  for (let i = 0; i < count * 3; i += 3) {
    for (let a = 0; a < 3; a++) {
      const v = positions[i + a];
      const lo = min[a] as number;
      const hi = max[a] as number;
      if (v < lo) min[a] = v;
      if (v > hi) max[a] = v;
    }
  }

  return { count, positions, colors, byteLength: buffer.byteLength, bounds: { min, max } };
}

/** 开发期的数据来源候选，按顺序尝试 */
const SOURCES: ReadonlyArray<{ url: string; label: string }> = [
  { url: '/__testdata/sample.ilcl', label: 'testdata/sample.ilcl（vite 中间件）' },
  { url: 'http://127.0.0.1:8123/api/cloud/sample', label: 'Go 服务 /api/cloud/sample' },
  { url: '/sample.ilcl', label: 'web/public/sample.ilcl' },
];

export interface LoadResult {
  cloud: IlcCloud;
  /** 实际命中的来源，显示在 HUD 上方便排查 */
  source: string;
}

/** 依次尝试候选来源，返回第一个成功的点云 */
export async function loadSampleCloud(): Promise<LoadResult> {
  const errors: string[] = [];

  for (const candidate of SOURCES) {
    try {
      const res = await fetch(candidate.url, { cache: 'no-store' });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const buffer = await res.arrayBuffer();
      const cloud = parseIlcl(buffer);
      return { cloud, source: candidate.label };
    } catch (error) {
      errors.push(`${candidate.url} → ${error instanceof Error ? error.message : String(error)}`);
    }
  }

  throw new Error(
    `拿不到 testdata/sample.ilcl，三种方式都失败了：\n  ${errors.join('\n  ')}`,
  );
}
