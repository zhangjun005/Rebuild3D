#!/usr/bin/env python
"""分割质量探针：跑 anime-seg（isnet_is ONNX）看角色抠图边界够不够干净。

用途：在把它接进点云管线之前，先判断 mask 质量——边界是否贴合线稿、
多人/复杂背景会不会失败。这是"边界带"方案的前置验证。

用法：
    python scripts/probe-seg.py <插画...>
    python scripts/probe-seg.py --dir testdata/illustrations

输出（out/seg/）：
    <name>.mask.png     二值 mask
    <name>.overlay.png  原图上叠 mask 边界（红色）—— 一眼能看出边界准不准
    contact-sheet.png   所有图拼版（避免逐张读图）
"""
from __future__ import annotations

import argparse
import time
from pathlib import Path

import numpy as np
import onnxruntime as ort
from PIL import Image, ImageDraw

MODEL = Path(r"E:\models\anime-seg\isnetis.onnx")
OUT = Path("out/seg")
MEAN = np.array([0.485, 0.456, 0.406], dtype=np.float32)
STD = np.array([0.229, 0.224, 0.225], dtype=np.float32)


def load_session() -> ort.InferenceSession:
    so = ort.SessionOptions()
    so.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
    so.intra_op_num_threads = 0  # 用满 CPU
    return ort.InferenceSession(str(MODEL), sess_options=so, providers=["CPUExecutionProvider"])


def segment(sess: ort.InferenceSession, img: Image.Image) -> tuple[np.ndarray, float]:
    """返回 (mask HxW bool, 耗时秒)。输入会被拉伸到模型尺寸（与该仓库一致）。"""
    in_name = sess.get_inputs()[0].name
    _, _, size, _ = sess.get_inputs()[0].shape
    size = int(size) if isinstance(size, int) else 1024

    rgb = img.convert("RGB")
    small = rgb.resize((size, size), Image.BILINEAR)
    x = np.asarray(small, dtype=np.float32) / 255.0
    x = (x - MEAN) / STD
    x = np.transpose(x, (2, 0, 1))[None, ...].astype(np.float32)

    t0 = time.perf_counter()
    out = sess.run(None, {in_name: x})[0]
    dt = time.perf_counter() - t0

    # 输出是 logits（或已 sigmoid），统一按 sigmoid 处理后再判断
    logits = np.asarray(out, dtype=np.float32).reshape(size, size)
    prob = 1.0 / (1.0 + np.exp(-logits))
    m = Image.fromarray((prob * 255).astype(np.uint8), "L").resize(rgb.size, Image.BILINEAR)
    mask = np.asarray(m, dtype=np.uint8) >= 128
    return mask, dt


def overlay(img: Image.Image, mask: np.ndarray) -> Image.Image:
    """原图上把 mask 边界描成红色，直观判断边界贴合度。"""
    base = img.convert("RGB").copy()
    h, w = mask.shape
    # 用形态学差找边界（不引 scipy/opencv）
    up = np.zeros_like(mask)
    down = np.zeros_like(mask)
    left = np.zeros_like(mask)
    right = np.zeros_like(mask)
    up[1:, :] = mask[:-1, :]
    down[:-1, :] = mask[1:, :]
    left[:, 1:] = mask[:, :-1]
    right[:, :-1] = mask[:, 1:]
    edge = mask & ~(up & down & left & right)
    arr = np.asarray(base, dtype=np.uint8).copy()
    arr[edge] = [255, 0, 0]
    return Image.fromarray(arr, "RGB")


def montage(items: list[tuple[str, Image.Image]], cols: int = 3) -> Image.Image:
    if not items:
        return Image.new("RGB", (1, 1))
    cell_w = 480
    cell_h = 360
    label_h = 22
    rows = (len(items) + cols - 1) // cols
    sheet = Image.new("RGB", (cols * cell_w, rows * (cell_h + label_h)), (28, 28, 30))
    draw = ImageDraw.Draw(sheet)
    for i, (name, im) in enumerate(items):
        r, c = divmod(i, cols)
        x = c * cell_w
        y = r * (cell_h + label_h)
        draw.text((x + 4, y + 4), name, fill=(240, 240, 240))
        thumb = im.copy()
        thumb.thumbnail((cell_w, cell_h), Image.LANCZOS)
        sheet.paste(thumb, (x, y + label_h))
    return sheet


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("images", nargs="*")
    ap.add_argument("--dir", help="目录（取 png/jpg/jpeg/webp）")
    args = ap.parse_args()

    files: list[Path] = [Path(p) for p in args.images]
    if args.dir:
        d = Path(args.dir)
        files += sorted(p for p in d.iterdir() if p.suffix.lower() in (".png", ".jpg", ".jpeg", ".webp"))

    if not files:
        print("没有输入图片")
        return 2

    OUT.mkdir(parents=True, exist_ok=True)
    sess = load_session()
    print(f"模型: {MODEL.name}  ({MODEL.stat().st_size / 1e6:.0f} MB)")
    size = sess.get_inputs()[0].shape[2]
    print(f"输入: {sess.get_inputs()[0].name} {sess.get_inputs()[0].shape}\n")

    items = []
    for p in files:
        try:
            img = Image.open(p)
            mask, dt = segment(sess, img)
            coverage = mask.mean() * 100
            Image.fromarray((mask * 255).astype(np.uint8), "L").save(OUT / f"{p.stem}.mask.png")
            ov = overlay(img, mask)
            ov.save(OUT / f"{p.stem}.overlay.png")
            items.append((f"{p.name} | {img.width}x{img.height} | 角色占 {coverage:.1f}% | {dt:.2f}s", ov))
            print(f"  {p.name:<28} 角色占比 {coverage:5.1f}%   CPU 推理 {dt:.2f}s")
        except Exception as e:  # noqa: BLE001
            print(f"  {p.name}: 失败 {e}")

    sheet = montage(items)
    sheet.save(OUT / "contact-sheet.png")
    print(f"\n拼版: {OUT / 'contact-sheet.png'}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
