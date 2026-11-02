#!/usr/bin/env python
"""探测 DA3MONO-LARGE 到底输出什么（这是几何约定的第一手依据）。

要回答的问题：
  1. depth 是**度量的**（米）还是相对的（归一化/视差）？→ pred.is_metric / scale_factor
  2. 有没有相机内参？→ pred.intrinsics（有的话就不该再用 focal_factor 猜）
  3. 有没有外参？→ pred.extrinsics
  4. depth 的数值分布：范围、单调性（是"距离"还是"逆深度/视差"）

用法：python scripts/probe-da3-output.py [图片]
"""
from __future__ import annotations

import sys
from pathlib import Path

import numpy as np

REPO = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(REPO / "sidecar" / "vendor" / "Depth-Anything-3" / "src"))

from PIL import Image  # noqa: E402

from depth_anything_3.api import DepthAnything3  # noqa: E402

MODEL = "depth-anything/DA3MONO-LARGE"
IMG = Path(sys.argv[1]) if len(sys.argv) > 1 else REPO / "testdata/illustrations/平涂131905131_p0.png"


def main() -> int:
    print(f"图片: {IMG.name}  {Image.open(IMG).size}")
    model = DepthAnything3.from_pretrained(MODEL).to("cuda").eval()
    img = Image.open(IMG).convert("RGB")
    pred = model.inference([img], process_res=1008)

    print("\n=== Predication 字段 ===")
    print(f"  is_metric      : {getattr(pred, 'is_metric', '<无此字段>')}")
    print(f"  scale_factor   : {getattr(pred, 'scale_factor', '<无此字段>')}")

    d = np.asarray(pred.depth)
    print(f"  depth.shape    : {d.shape}  dtype={d.dtype}")
    print(f"  范围           : [{d.min():.4f}, {d.max():.4f}]  中位 {np.median(d):.4f}")
    for p in (1, 5, 25, 50, 75, 95, 99):
        print(f"    p{p:<3}         : {np.percentile(d, p):.4f}")

    for name in ("intrinsics", "extrinsics", "conf", "sky", "aux"):
        v = getattr(pred, name, None)
        if v is None:
            print(f"  {name:<14} : None")
            continue
        a = np.asarray(v)
        print(f"  {name:<14} : shape={a.shape} dtype={a.dtype}")
        if name == "intrinsics":
            print(f"     K = \n{np.array2string(a[0], precision=2, suppress_small=True)}")
            # 由 K 反推 FOV，和"以长边为焦距"的假设对比
            H, W = a.shape[0] if False else (d.shape[1], d.shape[2])
            k = a[0]
            fx, fy, cx, cy = k[0, 0], k[1, 1], k[0, 2], k[1, 2]
            print(f"     fx={fx:.2f} fy={fy:.2f} cx={cx:.2f} cy={cy:.2f}")
            print(f"     图像 {W}x{H} → cx/W={cx/W:.4f}  cy/H={cy/H:.4f}")
            print(f"     水平 FOV = {2*np.degrees(np.arctan(W/2/fx)):.2f}°")
            print(f"     以长边为焦距假设 = max(W,H)={max(W,H)}，模型给 fx={fx:.1f}")
        elif name == "extrinsics":
            print(f"     E[0] = \n{np.array2string(a[0], precision=4, suppress_small=True)}")

    # 单调性检查：把深度当"距离"用是否合理
    print("\n=== 语义判断 ===")
    print("  若 is_metric=1 → depth 是米（真实距离），可直接 x=(u-cx)*d/f")
    print("  若 is_metric=0 → depth 是相对值，尺度任意；只能用来做「层序」与「视差」")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
