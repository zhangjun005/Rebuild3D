// ─────────────────────────────────────────────────────────────────────────────
// blue noise 生成脚本（void-and-cluster / Ulichney 1993）
//
// 产出：web/src/assets/blue-noise.png（默认 256×256，8 位灰度）
// 用途：docs/rendering-styles.md §2.1 / §8 的「屏幕空间点画」阈值纹理。
//
// 为什么自己生成：这是这条线唯一需要的外部素材（§8 末），
// 而自己生成可以保证「可重复、无版权、可换尺寸」。
//
// 运行：npm run gen:noise  （或 node scripts/gen-blue-noise.mjs [--size N]）
//
// 算法：
//   1. 随机撒 10% 的点，算高斯能量场（环形边界 = 可平铺）。
//   2. 反复「移除最紧的簇 → 填进最大的空」，直到两者重合（= 稳定的 blue noise 点集）。
//   3. 以该点集为起点，按「最紧簇 → 最小 rank」逐个移除，得到 0..M-1；
//      再从全空按「最大空 → 最小 rank」逐个填入，得到 M..N-1。
//   4. rank 归一化成 0..255 的阈值图 —— 任意阈值切出来的都是 blue noise 点集。
//
// 成本：O(N²) 的全局最大/最小扫描，256² 约 10~20 s，够用（一次生成，产物进仓库）。
// ─────────────────────────────────────────────────────────────────────────────

import { deflateSync } from 'node:zlib';
import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const OUT = resolve(HERE, '../src/assets/blue-noise.png');

// ── 参数 ────────────────────────────────────────────────────────────────────
const args = process.argv.slice(2);
const argOf = (name, fallback) => {
  const i = args.indexOf(`--${name}`);
  return i >= 0 && args[i + 1] !== undefined ? Number(args[i + 1]) : fallback;
};
const SIZE = argOf('size', 256); // 必须是 2 的幂（纹理可平铺重复）
const SIGMA = 1.5; // 能量场高斯 σ（越大越"蓝"，越小越容易出条纹）
const RADIUS = 6; // 核半径：σ=1.5 时 6 已经足够（±4σ）
const SEED = 20261101;

// ── 确定性 RNG（mulberry32）──────────────────────────────────────────────────
function mulberry32(seed) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ── void-and-cluster ────────────────────────────────────────────────────────
function voidAndCluster(size, sigma, radius, rng) {
  const N = size * size;
  const wrap = (v) => ((v % size) + size) % size;
  const idx = (x, y) => wrap(y) * size + wrap(x);

  // 高斯核（偏移列表，环形边界）
  const kdx = new Int32Array((2 * radius + 1) ** 2);
  const kdy = new Int32Array(kdx.length);
  const kw = new Float64Array(kdx.length);
  {
    let k = 0;
    for (let dy = -radius; dy <= radius; dy++) {
      for (let dx = -radius; dx <= radius; dx++) {
        kdx[k] = dx;
        kdy[k] = dy;
        kw[k] = Math.exp(-(dx * dx + dy * dy) / (2 * sigma * sigma));
        k++;
      }
    }
  }

  const energy = new Float64Array(N);
  const pattern = new Uint8Array(N);
  const rank = new Int32Array(N).fill(-1);

  const stamp = (p, sign) => {
    const px = p % size;
    const py = (p - px) / size;
    for (let k = 0; k < kw.length; k++) {
      energy[idx(px + kdx[k], py + kdy[k])] += sign * kw[k];
    }
  };

  const findTightestCluster = () => {
    let best = -Infinity;
    let bp = -1;
    for (let p = 0; p < N; p++) {
      if (pattern[p] === 1 && energy[p] > best) {
        best = energy[p];
        bp = p;
      }
    }
    return bp;
  };

  const findLargestVoid = () => {
    let best = Infinity;
    let bp = -1;
    for (let p = 0; p < N; p++) {
      if (pattern[p] === 0 && energy[p] < best) {
        best = energy[p];
        bp = p;
      }
    }
    return bp;
  };

  // 1. 初始随机点集（10%）
  const M = Math.round(N * 0.1);
  const order = new Int32Array(N);
  for (let p = 0; p < N; p++) order[p] = p;
  for (let i = N - 1; i > 0; i--) {
    const j = Math.floor(rng() * (i + 1));
    const t = order[i];
    order[i] = order[j];
    order[j] = t;
  }
  for (let i = 0; i < M; i++) {
    pattern[order[i]] = 1;
    stamp(order[i], +1);
  }

  // 2. 松弛到稳定点集
  const t0 = Date.now();
  let iters = 0;
  for (;;) {
    iters++;
    const c = findTightestCluster();
    if (c < 0) break;
    pattern[c] = 0;
    stamp(c, -1);
    const v = findLargestVoid();
    if (v === c) {
      // 最紧簇 == 最大空 → 已稳定
      pattern[c] = 1;
      stamp(c, +1);
      break;
    }
    pattern[v] = 1;
    stamp(v, +1);
  }
  process.stdout.write(
    `  松弛完成：${iters} 次迭代，${Date.now() - t0} ms，稳定点集 ${M} 点\n`,
  );

  // 3a. 从稳定点集里逐个移除最紧簇 → rank = M-1 .. 0
  const t1 = Date.now();
  let cur = M;
  while (cur > 0) {
    const c = findTightestCluster();
    pattern[c] = 0;
    stamp(c, -1);
    rank[c] = cur - 1;
    cur--;
  }

  // 3b. 从全空里逐个填入最大空 → rank = M .. N-1
  let filled = M;
  while (filled < N) {
    const v = findLargestVoid();
    pattern[v] = 1;
    stamp(v, +1);
    rank[v] = filled;
    filled++;
  }
  process.stdout.write(`  排序完成：${Date.now() - t1} ms\n`);

  // 4. rank → 0..255
  const out = new Uint8Array(N);
  const denom = N - 1;
  for (let p = 0; p < N; p++) out[p] = Math.round((rank[p] / denom) * 255);
  return out;
}

// ── CRC32 / PNG（8 位灰度，不引依赖）────────────────────────────────────────
const CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();

function crc32(buf) {
  let c = 0xffffffff;
  for (let i = 0; i < buf.length; i++) c = CRC_TABLE[(c ^ buf[i]) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function pngChunk(type, body) {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(body.length, 0);
  const t = Buffer.from(type, 'ascii');
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(Buffer.concat([t, body])), 0);
  return Buffer.concat([len, t, body, crc]);
}

function encodePngGray(size, data) {
  const raw = Buffer.alloc(size * (size + 1));
  for (let y = 0; y < size; y++) {
    raw[y * (size + 1)] = 0; // filter: none
    raw.set(data.subarray(y * size, (y + 1) * size), y * (size + 1) + 1);
  }
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(size, 0);
  ihdr.writeUInt32BE(size, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 0; // color type: grayscale
  ihdr[10] = 0; // compression
  ihdr[11] = 0; // filter
  ihdr[12] = 0; // interlace
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    pngChunk('IHDR', ihdr),
    pngChunk('IDAT', deflateSync(raw, { level: 9 })),
    pngChunk('IEND', Buffer.alloc(0)),
  ]);
}

// ── 质量自检：blue noise 的低频能量应远低于白噪声 ───────────────────────────
function blockStd(data, size, block) {
  const n = size / block;
  let sum = 0;
  let sum2 = 0;
  const count = n * n;
  for (let by = 0; by < n; by++) {
    for (let bx = 0; bx < n; bx++) {
      let acc = 0;
      for (let y = 0; y < block; y++) {
        for (let x = 0; x < block; x++) acc += data[(by * block + y) * size + bx * block + x];
      }
      const m = acc / (block * block) / 255;
      sum += m;
      sum2 += m * m;
    }
  }
  const mean = sum / count;
  return Math.sqrt(Math.max(0, sum2 / count - mean * mean));
}

// ── main ────────────────────────────────────────────────────────────────────
if (!Number.isInteger(SIZE) || (SIZE & (SIZE - 1)) !== 0 || SIZE < 32) {
  console.error(`--size 必须是 ≥32 的 2 的幂，收到 ${SIZE}`);
  process.exit(1);
}

console.log(`生成 blue noise ${SIZE}×${SIZE}（σ=${SIGMA}, r=${RADIUS}, seed=${SEED}）`);
const tStart = Date.now();
const data = voidAndCluster(SIZE, SIGMA, RADIUS, mulberry32(SEED));

const blueLow = blockStd(data, SIZE, 8);
// 同分布白噪声作为对照
const white = new Uint8Array(SIZE * SIZE);
{
  const rng = mulberry32(SEED + 1);
  for (let i = 0; i < white.length; i++) white[i] = Math.floor(rng() * 256);
}
const whiteLow = blockStd(white, SIZE, 8);

mkdirSync(dirname(OUT), { recursive: true });
const png = encodePngGray(SIZE, data);
writeFileSync(OUT, png);

console.log(`  低频块标准差（8×8 平均，越小越蓝）`);
console.log(`    blue noise : ${blueLow.toFixed(4)}`);
console.log(`    白噪声对照 : ${whiteLow.toFixed(4)}`);
console.log(`    比值       : ${(blueLow / whiteLow).toFixed(2)}× （理想 < 0.5）`);
console.log(`  已写出 ${OUT}（${(png.length / 1024).toFixed(1)} KB）`);
console.log(`  总耗时 ${((Date.now() - tStart) / 1000).toFixed(1)} s`);
if (blueLow > whiteLow * 0.7) {
  console.error('  ⚠ 低频能量偏高，blue noise 质量不达标');
  process.exit(2);
}
