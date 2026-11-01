#!/usr/bin/env python
"""用 float32 原始深度数据做量化分析，绕开会损失信息的可视化。

写这个脚本的原因：go/no-go 是靠"看拼版图"判的，但拼版图有三处信息损失：
  1. 归一化时按 1/99 分位裁剪（丢掉两端各 1%）
  2. 8 位量化（256 级）
  3. 拼版时缩到 300 px 高（细节糊掉）

所以要回答「结论是不是可视化假象」，必须回到 probe 存下来的 .depth.npy 原始 float32。

分析三件事：
  A. 真实动态范围：是不是"范围太小"导致看起来平？
  B. 8 位量化损失：256 级到底够不够表达这个深度分布？
  C. 主体内部结构：主体区域内到底有没有深度变化，还是真的一块板？

用法：
    python scripts/analyze-depth.py                    # 默认 out/probe
    python scripts/analyze-depth.py --dir out/probe
"""
from __future__ import annotations

import argparse
from pathlib import Path

import numpy as np


def kmeans_1d(x: np.ndarray, k: int = 3, iters: int = 30) -> tuple[np.ndarray, np.ndarray]:
    """一维 k-means，返回 (聚类中心升序, 各簇占比)。用来数"深度分成几层"。"""
    if x.size == 0:
        return np.array([]), np.array([])
    # 分位数初始化，比随机稳
    centers = np.percentile(x, np.linspace(10, 90, k))
    for _ in range(iters):
        d = np.abs(x[:, None] - centers[None, :])
        lab = d.argmin(axis=1)
        new = np.array([x[lab == i].mean() if np.any(lab == i) else centers[i] for i in range(k)])
        if np.allclose(new, centers, atol=1e-6):
            centers = new
            break
        centers = new
    d = np.abs(x[:, None] - centers[None, :])
    lab = d.argmin(axis=1)
    weights = np.array([(lab == i).mean() for i in range(k)])
    order = np.argsort(centers)
    return centers[order], weights[order]


def analyze(path: Path) -> dict | None:
    d = np.load(path).astype(np.float64).ravel()
    d = d[np.isfinite(d)]
    if d.size < 100:
        return None

    p1, p5, p50, p95, p99 = np.percentile(d, [1, 5, 50, 95, 99])
    rng = p99 - p1
    if rng < 1e-9:
        rng = 1e-9

    # ── B. 8 位量化（模拟我拼版图时的做法）：p1..p99 映射到 0..255 ──
    norm = np.clip((d - p1) / rng, 0, 1)
    q = np.round(norm * 255).astype(np.uint8)
    levels_used = int(np.unique(q).size)

    # ── C. 主体内部结构 ──
    # 插画里主体通常更近（深度值更小）。取最近的 25% 像素当"主体"。
    thr = np.percentile(d, 25)
    subject = d[d <= thr]
    if subject.size < 50:
        sub_iqr_ratio = float("nan")
    else:
        sub_iqr = np.percentile(subject, 75) - np.percentile(subject, 25)
        sub_iqr_ratio = sub_iqr / rng

    # 主体 vs 背景的分离度：最近 25% 与最远 25% 的均值差 / 全局范围
    near = d[d <= np.percentile(d, 25)].mean()
    far = d[d >= np.percentile(d, 75)].mean()
    separation = (far - near) / rng

    centers, weights = kmeans_1d(d, k=3)

    return {
        "name": path.stem.replace(".depth", ""),
        "min": d.min(),
        "p1": p1,
        "p50": p50,
        "p99": p99,
        "max": d.max(),
        "range_p1_p99": rng,
        "ratio_p99_p1": p99 / max(p1, 1e-9),
        "std": d.std(),
        "levels_8bit": levels_used,
        "sub_iqr_ratio": sub_iqr_ratio,
        "separation": separation,
        "layers": centers,
        "layer_weights": weights,
    }


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--dir", default="out/probe")
    args = ap.parse_args()

    files = sorted(Path(args.dir).glob("*.depth.npy"))
    if not files:
        print(f"{args.dir} 下没有 *.depth.npy（先跑 scripts/probe-depth.py）")
        return 1

    rows = [r for r in (analyze(f) for f in files) if r]

    print("=" * 104)
    print("A. 真实动态范围（有没有可能「范围太小」）")
    print("=" * 104)
    print(f"{'#':>2} {'文件':<22} {'min':>8} {'p1':>8} {'p50':>8} {'p99':>8} {'max':>9} "
          f"{'p99-p1':>9} {'p99/p1':>7}")
    for i, r in enumerate(rows):
        print(f"{i:>2} {r['name'][:22]:<22} {r['min']:>8.3f} {r['p1']:>8.3f} {r['p50']:>8.3f} "
              f"{r['p99']:>8.3f} {r['max']:>9.3f} {r['range_p1_p99']:>9.3f} {r['ratio_p99_p1']:>7.1f}")

    print()
    print("=" * 104)
    print("B. 8 位量化损失（拼版图只用了 256 级；levels 越接近 256 越说明量化不是瓶颈）")
    print("=" * 104)
    print(f"{'#':>2} {'文件':<22} {'std':>8} {'8位用到的级数':>14} {'占 256 的比例':>13}")
    for i, r in enumerate(rows):
        print(f"{i:>2} {r['name'][:22]:<22} {r['std']:>8.3f} {r['levels_8bit']:>14d} "
              f"{r['levels_8bit']/256*100:>12.1f}%")

    print()
    print("=" * 104)
    print("C. 主体内部结构（关键！sub_iqr/separation 越小，主体越像一块平板）")
    print("=" * 104)
    print(f"{'#':>2} {'文件':<22} {'主体内IQR/全幅':>15} {'主体vs背景分离度':>17} "
          f"{'三层均值(near→far)':<34} {'三层占比'}")
    for i, r in enumerate(rows):
        lay = " ".join(f"{c:>8.2f}" for c in r["layers"])
        wts = " ".join(f"{w*100:>5.0f}%" for w in r["layer_weights"])
        print(f"{i:>2} {r['name'][:22]:<22} {r['sub_iqr_ratio']:>15.4f} {r['separation']:>17.3f} "
              f"{lay:<34} {wts}")

    print()
    print("读法：")
    print("  sub_iqr_ratio < 0.02  → 主体内部几乎没有深度变化，就是一块板（剪影）")
    print("  sub_iqr_ratio > 0.06  → 主体内部有真实结构")
    print("  levels_8bit 接近 256  → 8 位量化不是信息瓶颈，拼版图看得见的损失主要是缩放")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
