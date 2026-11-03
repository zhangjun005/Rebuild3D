#!/usr/bin/env python
"""LaMa 补洞质量探针：分割出角色 → 抠掉 → 补背景。

这是分层 2.5D 的关键前置验证：如果补出来的背景可信，那么转动时露出的就是背景
而不是纸（空洞问题解决）；如果补得难看，就得换更重的模型或退回限制角度。

用法：python scripts/probe-inpaint.py [图片...]
输出：out/inpaint/<name>.inpainted.png 与 contact-sheet.png
"""
from __future__ import annotations

import sys
import time
from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw

REPO = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(REPO / "sidecar"))

from inpaint_engine import InpaintEngine  # noqa: E402
from seg_engine import SegEngine  # noqa: E402

OUT = REPO / "out/inpaint"


def montage(items: list[tuple[str, Image.Image]], cols: int = 3) -> Image.Image:
    if not items:
        return Image.new("RGB", (1, 1))
    cell_w, cell_h, label_h = 460, 460, 22
    rows = (len(items) + cols - 1) // cols
    sheet = Image.new("RGB", (cols * cell_w, rows * (cell_h + label_h)), (28, 28, 30))
    draw = ImageDraw.Draw(sheet)
    for i, (name, im) in enumerate(items):
        r, c = divmod(i, cols)
        x, y = c * cell_w, r * (cell_h + label_h)
        draw.text((x + 4, y + 4), name, fill=(240, 240, 240))
        thumb = im.copy()
        thumb.thumbnail((cell_w, cell_h), Image.LANCZOS)
        sheet.paste(thumb, (x, y + label_h))
    return sheet


def main() -> int:
    files = [Path(p) for p in sys.argv[1:]]
    if not files:
        files = sorted((REPO / "testdata/illustrations").glob("*.png")) + sorted(
            (REPO / "testdata/illustrations").glob("*.jpg")
        )
    if not files:
        print("没有输入图片")
        return 2

    OUT.mkdir(parents=True, exist_ok=True)
    print("加载分割模型...")
    seg = SegEngine()
    print("加载 LaMa...")
    inp = InpaintEngine()

    items: list[tuple[str, Image.Image]] = []
    for p in files:
        try:
            data = p.read_bytes()
            img = Image.open(p).convert("RGB")

            t0 = time.perf_counter()
            mask_bytes, mw, mh = seg.segment(data)
            t_seg = time.perf_counter() - t0

            mask = np.frombuffer(mask_bytes, dtype=np.uint8).reshape(mh, mw)
            char_ratio = (mask == 255).mean() * 100
            if char_ratio > 90:
                print(f"  {p.name}: 分割失败（角色占 {char_ratio:.1f}%），跳过")
                continue

            t0 = time.perf_counter()
            rgb, W, H = inp.inpaint(data, mask_bytes, mw, mh)
            t_inp = time.perf_counter() - t0

            out = Image.frombytes("RGB", (W, H), rgb)
            out.save(OUT / f"{p.stem}.inpainted.png")

            # 三联：原图 / 角色 mask / 补完
            mvis = Image.fromarray(mask, "L").convert("RGB")
            items += [
                (f"{p.name} 原图", img),
                (f"角色 mask {char_ratio:.0f}%", mvis),
                (f"补完 {t_seg:.1f}s+{t_inp:.1f}s", out),
            ]
            print(f"  {p.name:<28} 角色 {char_ratio:5.1f}%  分割 {t_seg:.1f}s  补洞 {t_inp:.1f}s")
        except Exception as e:  # noqa: BLE001
            print(f"  {p.name}: 失败 {type(e).__name__}: {e}")

    sheet = montage(items)
    sheet.save(OUT / "contact-sheet.png")
    print(f"\n拼版: {OUT / 'contact-sheet.png'}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
