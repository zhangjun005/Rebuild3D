#!/usr/bin/env python
"""对比两份 ILC1 点云「点落在图像边缘上」的程度。

用途：独立验证 geometry/structure-aware-sampling 是否真的把点搬到了强边缘上，
不依赖渲染层截图（也不需要浏览器）。

原理：
  1. 解码 ILC1 → 3D 点
  2. 按反投影的逆变换还原每个点的**源像素坐标**
     （约定见 docs/selection.md §4.4：x=(u+0.5-cx)*d/fx, y=(cy-v-0.5)*d/fy, z=d）
  3. 在原图上算灰度 Sobel 梯度幅值，按 p99 归一
  4. 统计每个点所在位置的梯度值 → 比较两份点云的分布

如果 structure 模式有效：它采到的点的梯度均值/高分位应显著高于 depthpixel 模式。

用法：
    python scripts/compare-ilcl.py out/sample-depthpixel.ilcl out/sample-structure.ilcl \
        --image testdata/illustrations/平涂131905131_p0.png --depth 504x504
"""
from __future__ import annotations

import argparse
import struct
import sys
from pathlib import Path

import numpy as np
from PIL import Image


def decode_ilc1(path: Path) -> tuple[np.ndarray, np.ndarray]:
    raw = path.read_bytes()
    if raw[:4] != b"ILC1":
        raise ValueError(f"{path} 不是 ILC1（magic={raw[:4]!r}）")
    (n,) = struct.unpack("<I", raw[4:8])
    need = 8 + 15 * n
    if len(raw) < need:
        raise ValueError(f"{path} 被截断：需要 {need}，实际 {len(raw)}")
    pos = np.frombuffer(raw, dtype="<f4", count=n * 3, offset=8).reshape(n, 3)
    col = np.frombuffer(raw, dtype="u1", count=n * 3, offset=8 + n * 12).reshape(n, 3)
    return pos.astype(np.float64), col


def sobel_raw(gray: np.ndarray) -> np.ndarray:
    """灰度图 → 原始 Sobel 梯度幅值（不归一化，保留绝对量级）。"""
    g = gray.astype(np.float32)
    k = np.array([1.0, 2.0, 1.0], dtype=np.float32) / 4.0
    g = np.apply_along_axis(lambda r: np.convolve(r, k, mode="same"), 1, g)
    g = np.apply_along_axis(lambda c: np.convolve(c, k, mode="same"), 0, g)

    gx = np.zeros_like(g)
    gy = np.zeros_like(g)
    gx[:, 1:-1] = g[:, 2:] - g[:, :-2]
    gy[1:-1, :] = g[2:, :] - g[:-2, :]
    return np.hypot(gx, gy)


def edge_mask(mag: np.ndarray, top_pct: float) -> np.ndarray:
    """按「梯度最强的 top_pct%」定义边缘掩码。

    比"除以 p99 再比阈值"稳健得多：后者在扁平插画上会让绝大多数像素变成 0，
    度量失去分辨力（实测 p90 只有 0.0043，等于全是 0）。
    """
    thr = np.percentile(mag, 100.0 - top_pct)
    return mag >= thr


def recover_uv(pos: np.ndarray, depth_w: int, depth_h: int, focal: float) -> tuple[np.ndarray, np.ndarray]:
    """反投影的逆：3D 点 → 深度图像素坐标。"""
    cx, cy = depth_w / 2.0, depth_h / 2.0
    z = np.maximum(pos[:, 2], 1e-6)
    u = pos[:, 0] * focal / z + cx - 0.5
    v = cy - pos[:, 1] * focal / z - 0.5
    return u, v


def sample(gray_map: np.ndarray, u: np.ndarray, v: np.ndarray) -> np.ndarray:
    h, w = gray_map.shape
    ui = np.clip(np.round(u).astype(np.int64), 0, w - 1)
    vi = np.clip(np.round(v).astype(np.int64), 0, h - 1)
    return gray_map[vi, ui]


def analyze(path: Path, gray_map: np.ndarray, depth_w: int, depth_h: int, focal: float) -> dict:
    pos, _ = decode_ilc1(path)
    n = pos.shape[0]
    u, v = recover_uv(pos, depth_w, depth_h, focal)

    # 还原到原图坐标（深度图是原图按长边缩放后对齐 14 得到的）
    orig_h, orig_w = gray_map.shape
    sx = orig_w / depth_w
    sy = orig_h / depth_h
    vals = sample(gray_map, u * sx, v * sy)

    bbox_min = pos.min(axis=0)
    bbox_max = pos.max(axis=0)

    return {
        "name": path.name,
        "n": n,
        "bytes": path.stat().st_size,
        "grad_mean": float(vals.mean()),
        "grad_p50": float(np.percentile(vals, 50)),
        "grad_p90": float(np.percentile(vals, 90)),
        "edge_ratio_gt50": float((vals > 0.5).mean()),
        "edge_ratio_gt20": float((vals > 0.2).mean()),
        "bbox_min": bbox_min,
        "bbox_max": bbox_max,
        # 覆盖面积：点在有边缘的区域占比
        "vals": vals,
    }


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("a", help="基线 ILC1（期望 edge_ratio 较低）")
    ap.add_argument("b", help="对照 ILC1（structure 模式）")
    ap.add_argument("--image", required=True, help="源插画")
    ap.add_argument("--depth", required=True, help="深度图尺寸，如 504x504")
    ap.add_argument("--focal-factor", type=float, default=1.0)
    args = ap.parse_args()

    dw, dh = (int(x) for x in args.depth.lower().split("x"))
    focal = args.focal_factor * max(dw, dh)

    img = Image.open(args.image).convert("L")
    gray = np.asarray(img, dtype=np.float32)
    grad = sobel_norm(gray)

    a = analyze(Path(args.a), grad, dw, dh, focal)
    b = analyze(Path(args.b), grad, dw, dh, focal)

    print(f"源图: {args.image}  {img.width}x{img.height}")
    print(f"深度: {dw}x{dh}  focal={focal:.1f}  (focal_factor={args.focal_factor})")
    print()
    print(f"{'指标':<22} {'A: ' + a['name']:<34} {'B: ' + b['name']:<34} {'B/A':>8}")
    print("-" * 100)

    def row(label: str, ka: str, kb: str | None = None, fmt: str = "{:.4f}", ratio: bool = True):
        kb = kb or ka
        va, vb = a[ka], b[kb]
        r = (vb / va) if (ratio and va) else float("nan")
        rs = f"{r:>8.2f}" if ratio else f"{'':>8}"
        print(f"{label:<22} {fmt.format(va):<34} {fmt.format(vb):<34} {rs}")

    row("点数", "n", fmt="{:.0f}", ratio=True)
    row("ILC1 字节", "bytes", fmt="{:,.0f}", ratio=True)
    print()
    row("梯度均值", "grad_mean")
    row("梯度 p50", "grad_p50")
    row("梯度 p90", "grad_p90")
    row("落在强边缘点占比(>)", "edge_ratio_gt50")
    row("落在中边缘点占比(>)", "edge_ratio_gt20")

    print()
    print("bbox A:", np.round(a["bbox_min"], 3), "→", np.round(a["bbox_max"], 3))
    print("bbox B:", np.round(b["bbox_min"], 3), "→", np.round(b["bbox_max"], 3))

    print()
    gain = b["edge_ratio_gt50"] / a["edge_ratio_gt50"] if a["edge_ratio_gt50"] else float("inf")
    print("=" * 60)
    print(f"强边缘点占比提升: {a['edge_ratio_gt50']*100:.2f}% → {b['edge_ratio_gt50']*100:.2f}%  （×{gain:.2f}）")
    if gain > 1.5:
        print("判定: 结构采样**生效**（点确实搬到了边缘上）")
    elif gain > 1.05:
        print("判定: 略有提升但不显著，需要调大 edge_weight 或检查 workRes")
    else:
        print("判定: **未见效果**，结构采样可能没生效")
    print("=" * 60)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
