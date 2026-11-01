#!/usr/bin/env python
"""go/no-go 探针：拿插画跑 DA3 单目深度，判断"深度能不能用"。

这是整条线的第一道关卡（见 docs/scope.md §5 验证顺序第 1 条）。
如果插画的深度退化成"一张纸片"，几何点云方案就要整体退回 2.5D 分层视差。

用法（务必用 illust3d 环境的 python）：
    conda activate illust3d
    python scripts/probe-depth.py testdata/illustrations

    # 或指定模型 / 分辨率
    python scripts/probe-depth.py testdata/illustrations --model depth-anything/DA3SMALL --res 504

输出到 out/probe/：
    <name>.depth.npy          原始 float32 深度（后续几何用这个，不要用 png）
    <name>.depth.png          8 位灰度（近亮远暗）
    <name>.depth_color.png    伪彩色，便于肉眼判断分层
    report.json               汇总指标

判读标准（人眼看 color png）：
    ✅ 能用     ：前景/中景/背景有明显分层，物体有体积，边缘有前后关系
    ⚠️ 勉强     ：分层能看出来但很糊，物体像浮雕
    ❌ 不能用   ：整张近乎单色（纸片），或大面积错乱
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import time
from pathlib import Path

# ── 必须先把 vendor 源码加进 path：DA3 不在 site-packages 里 ──
ROOT = Path(__file__).resolve().parent.parent
VENDOR_SRC = ROOT / "sidecar" / "vendor" / "Depth-Anything-3" / "src"
if VENDOR_SRC.is_dir():
    sys.path.insert(0, str(VENDOR_SRC))
else:
    sys.exit(f"找不到 DA3 源码：{VENDOR_SRC}\n先跑 scripts/setup-env.ps1")

# ── 缓存位置兜底：即使没走 activate.d，也别让 HuggingFace 落 C 盘 ──
os.environ.setdefault("HF_HOME", r"E:\models\hf")
os.environ.setdefault("HF_ENDPOINT", "https://hf-mirror.com")
os.environ.setdefault("TORCH_HOME", r"E:\models\torch")

import numpy as np
import torch
from PIL import Image

IMG_EXT = {".png", ".jpg", ".jpeg", ".webp", ".bmp", ".tif", ".tiff"}


def colorize(norm: np.ndarray) -> np.ndarray:
    """把 [0,1] 的归一化深度映射成伪彩色（简易 viridis），不依赖 matplotlib。"""
    stops = np.array(
        [
            [68, 1, 84],
            [59, 82, 139],
            [33, 145, 140],
            [94, 201, 98],
            [253, 231, 37],
        ],
        dtype=np.float32,
    )
    pos = np.array([0.0, 0.25, 0.5, 0.75, 1.0], dtype=np.float32)
    flat = norm.ravel()
    out = np.empty((flat.size, 3), dtype=np.float32)
    for c in range(3):
        out[:, c] = np.interp(flat, pos, stops[:, c])
    return out.reshape(norm.shape + (3,)).astype(np.uint8)


def stats(a: np.ndarray) -> dict:
    finite = a[np.isfinite(a)]
    if finite.size == 0:
        return {"error": "全部非有限值"}
    q = np.percentile(finite, [0, 1, 5, 50, 95, 99, 100])
    return {
        "min": float(q[0]),
        "p01": float(q[1]),
        "p05": float(q[2]),
        "median": float(q[3]),
        "p95": float(q[4]),
        "p99": float(q[5]),
        "max": float(q[6]),
        "mean": float(finite.mean()),
        "std": float(finite.std()),
        "nan_ratio": float(1.0 - finite.size / a.size),
    }


def build_contact_sheet(rows, out_path: Path, tile_h: int = 300, pad: int = 6, label_h: int = 20) -> None:
    """把「原图 | 深度伪彩」逐行拼成一张对照图。

    为什么要这个：逐张看图很贵。拼成一张后，一次就能判断"不同画风的深度表现差异"
    ——而这正是 go/no-go 要看的东西。行首标序号，控制台打印 序号↔文件名 映射。

    rows: list of (原始 PIL.Image, 深度伪彩 PIL.Image)
    """
    from PIL import ImageDraw

    if not rows:
        return

    widths = []
    for orig, dep in rows:
        w1 = max(1, int(orig.width * tile_h / orig.height))
        w2 = max(1, int(dep.width * tile_h / dep.height))
        widths.append((w1, w2))

    row_w = max(a + b for a, b in widths) + pad
    total_h = len(rows) * (tile_h + label_h)
    sheet = Image.new("RGB", (row_w, total_h), (250, 248, 244))
    draw = ImageDraw.Draw(sheet)

    y = 0
    for idx, ((orig, dep), (w1, w2)) in enumerate(zip(rows, widths)):
        o = orig.convert("RGB").resize((w1, tile_h), Image.LANCZOS)
        p = dep.convert("RGB").resize((w2, tile_h), Image.LANCZOS)
        sheet.paste(o, (0, y))
        sheet.paste(p, (w1 + pad, y))
        # 序号只用 ASCII，避免默认字体缺中文字形
        draw.text((3, y + tile_h + 4), f"{idx:02d}  orig | depth", fill=(20, 20, 20))
        y += tile_h + label_h

    sheet.save(out_path)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("input", help="图片文件或目录")
    ap.add_argument("--model", default="depth-anything/DA3MONO-LARGE")
    ap.add_argument("--res", type=int, default=504, help="process_res，默认 504")
    ap.add_argument("--out", default="out/probe")
    ap.add_argument("--tile-h", type=int, default=300, help="拼版图每行的高度（像素）")
    ap.add_argument("--no-montage", action="store_true", help="不生成拼版图")
    args = ap.parse_args()

    src = Path(args.input)
    if not src.exists():
        sys.exit(f"输入不存在：{src}")
    files = [src] if src.is_file() else sorted(p for p in src.rglob("*") if p.suffix.lower() in IMG_EXT)
    if not files:
        sys.exit(f"{src} 下没有找到图片")

    outdir = Path(args.out)
    outdir.mkdir(parents=True, exist_ok=True)

    print(f"设备 : {torch.cuda.get_device_name(0) if torch.cuda.is_available() else 'CPU'}")
    print(f"模型 : {args.model}")
    print(f"图片 : {len(files)} 张")
    print(f"输出 : {outdir.resolve()}")
    print()

    from depth_anything_3.api import DepthAnything3  # noqa: E402

    t0 = time.time()
    model = DepthAnything3.from_pretrained(args.model).to("cuda")
    model.eval()
    print(f"[加载] {time.time() - t0:.1f}s")
    if torch.cuda.is_available():
        torch.cuda.reset_peak_memory_stats()
        print(f"[显存] 加载后已分配 {torch.cuda.memory_allocated()/2**20:.0f} MiB")
    print()

    report = {"model": args.model, "process_res": args.res, "images": {}}
    montage_rows = []
    order = []

    for i, f in enumerate(files, 1):
        name = f.stem
        print(f"[{i}/{len(files)}] {f.name}")
        try:
            img = Image.open(f).convert("RGB")
        except Exception as e:  # noqa: BLE001
            print(f"   !! 打不开：{e}")
            continue

        t = time.time()
        with torch.inference_mode():
            pred = model.inference([img], process_res=args.res)
        dt = time.time() - t

        depth = np.asarray(pred.depth)[0].astype(np.float32)  # (H, W)
        H, W = depth.shape
        conf = getattr(pred, "conf", None)
        conf_arr = None
        if conf is not None:
            conf_arr = np.asarray(conf)[0].astype(np.float32)

        dstat = stats(depth)
        np.save(outdir / f"{name}.depth.npy", depth)

        # 归一化：用 1/99 分位裁剪，避免极端值吃掉对比度
        lo, hi = np.percentile(depth[np.isfinite(depth)], [1, 99])
        if hi - lo < 1e-9:
            norm = np.zeros_like(depth)
        else:
            norm = np.clip((depth - lo) / (hi - lo), 0, 1)
        # 近亮远暗
        gray = ((1.0 - norm) * 255).astype(np.uint8)
        Image.fromarray(gray).save(outdir / f"{name}.depth.png")
        color_img = Image.fromarray(colorize(norm))
        color_img.save(outdir / f"{name}.depth_color.png")
        montage_rows.append((img.copy(), color_img.copy()))
        order.append(name)

        peak = torch.cuda.max_memory_allocated() / 2**20 if torch.cuda.is_available() else 0
        intr = getattr(pred, "intrinsics", None)
        extr = getattr(pred, "extrinsics", None)

        print(f"   尺寸 {W}x{H}  耗时 {dt:.2f}s  峰值显存 {peak:.0f} MiB")
        print(f"   深度 min={dstat['min']:.3f} p50={dstat['median']:.3f} max={dstat['max']:.3f} "
              f"std={dstat['std']:.3f} nan={dstat['nan_ratio']*100:.1f}%")
        print(f"   conf: {'有 ' + str(conf_arr.shape) if conf_arr is not None else '**无**'}"
              f"   intrinsics: {'有' if intr is not None else '无'}"
              f"   extrinsics: {'有' if extr is not None else '无'}")

        if conf_arr is not None:
            cstat = stats(conf_arr)
            report["images"][name] = {"depth": dstat, "conf": cstat, "seconds": dt,
                                      "size": [W, H], "peak_mib": peak}
        else:
            report["images"][name] = {"depth": dstat, "conf": None, "seconds": dt,
                                      "size": [W, H], "peak_mib": peak}

    # 汇总
    if report["images"]:
        peaks = [v["peak_mib"] for v in report["images"].values()]
        report["peak_mib_max"] = max(peaks)
        stds = [v["depth"]["std"] for v in report["images"].values()]
        report["depth_std_range"] = [min(stds), max(stds)]
        print()
        print("=" * 66)
        print(f"峰值显存     : {max(peaks):.0f} MiB")
        print(f"深度 std 区间: {min(stds):.3f} ~ {max(stds):.3f}")

        # 一张对照表：序号 / 文件名 / std / 分位跨度
        print()
        print(f"{'#':>2}  {'文件':<26} {'std':>6}  {'p05':>7} {'p95':>7}  {'跨度比':>6}")
        for i, nm in enumerate(order):
            d = report["images"][nm]["depth"]
            span = (d["p95"] - d["p05"]) / max(abs(d["median"]), 1e-6)
            print(f"{i:>2}  {nm[:26]:<26} {d['std']:>6.3f}  {d['p05']:>7.3f} {d['p95']:>7.3f}  {span:>6.2f}")

        if not args.no_montage:
            sheet = outdir / "contact-sheet.png"
            build_contact_sheet(montage_rows, sheet, tile_h=args.tile_h)
            report["contact_sheet"] = sheet.name
            print()
            print(f"拼版图 -> {sheet.resolve()}")
            print("  每行 = 左边原图 | 右边深度伪彩，行首序号对应上表")

        print()
        print("关键判读：")
        print("  std < 0.05 通常已退化成一团；但 std 正常也可能几何错乱，必须看图。")
        print("  看图重点：前景/中景/背景有没有明显分层？物体有没有体积？边缘有前后关系吗？")
        print("=" * 66)

    (outdir / "report.json").write_text(
        json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8"
    )
    print(f"已写 {outdir / 'report.json'}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
