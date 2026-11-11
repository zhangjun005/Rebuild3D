"""LaMa 补洞引擎（big-lama，ONNX + onnxruntime）。
# @kron:intent geometry/hole-filling
# @kron:assumes lama-cpu-speed-acceptable

用途：把角色抠掉之后，补出它后面的背景。
这是分层 2.5D 的关键一步——原图里被角色挡住的那部分像素**根本不存在**，
层一错动就会露出空洞；补完之后背景层是完整的，转动时看到的是背景而不是纸。

模型：Carve/LaMa-ONNX 的 lama_fp32.onnx（Apache-2.0）。
输入 image [B,3,512,512] float、mask [B,1,512,512] float（1=要补的洞），
输出 out [B,3,512,512] float。

为什么用 LaMa 而不是 SD inpaint：
  - 198MB vs 2~4GB；
  - 非扩散、单次前向，CPU 也能跑（约 1~2s）；
  - 对平涂/渐变背景补得干净，正好是插画的主场景。

局部裁剪策略：整图缩到 512 会糊掉细节，所以先按 mask 的包围盒裁一块（带边距），
缩到 512 补完再贴回去——背景细节基本保住。
"""

from __future__ import annotations

import io
import os
import time
from pathlib import Path

import numpy as np
from PIL import Image

DEFAULT_LAMA_MODEL = os.environ.get(
    "ILLUST3D_LAMA_MODEL", r"E:\models\lama\lama_fp32.onnx"
)

MODEL_SIZE = 512
# 裁剪时在 mask 包围盒外留的边距（相对包围盒边长的比例）。
CROP_MARGIN = 0.25


class InpaintEngine:
    """懒加载的 LaMa 补洞引擎。线程不安全，由 server 的单线程循环保证串行。"""

    def __init__(self, model_path: str = DEFAULT_LAMA_MODEL, enabled: bool = True) -> None:
        self.model_path = Path(model_path)
        self.enabled = enabled and self.model_path.exists()
        self._sess = None
        self._calls = 0
        self._last_seconds = 0.0

    # ── 生命周期 ──────────────────────────────────────────────────────────
    def _ensure(self) -> None:
        if self._sess is not None:
            return
        if not self.enabled:
            raise RuntimeError(f"LaMa 模型不可用（{self.model_path}）")
        import onnxruntime as ort  # 延迟导入

        so = ort.SessionOptions()
        so.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
        so.intra_op_num_threads = 0
        self._sess = ort.InferenceSession(
            str(self.model_path), sess_options=so, providers=["CPUExecutionProvider"]
        )
        # 512×512 的 LaMa 在 CPU 上会很慢（几十秒），所以尽量用 GPU provider（如果装了）。
        providers = self._sess.get_providers()
        if "CUDAExecutionProvider" not in providers and os.environ.get("ILLUST3D_LAMA_GPU") == "1":
            self._sess = ort.InferenceSession(
                str(self.model_path), sess_options=so,
                providers=["CUDAExecutionProvider", "CPUExecutionProvider"],
            )

    def status(self) -> dict:
        return {
            "enabled": bool(self.enabled),
            "loaded": self._sess is not None,
            "model": self.model_path.name,
            "input_size": MODEL_SIZE,
            "calls": self._calls,
            "last_seconds": round(self._last_seconds, 3),
            "providers": self._sess.get_providers() if self._sess is not None else [],
        }

    def unload(self) -> None:
        self._sess = None

    # ── 推理 ──────────────────────────────────────────────────────────────
    def inpaint(self, image_bytes: bytes, mask_bytes: bytes, mw: int, mh: int) -> tuple[bytes, int, int]:
        """把 image 里 mask=255 的区域补掉，返回 RGB 字节 + 宽高。

        输入：
          image_bytes : 原图（任意尺寸）
          mask_bytes  : u8[mw*mh]，255=要补的洞（角色），其它=保留
        输出：补完的**整图**（原尺寸）RGB 字节。
        """
        self._ensure()
        assert self._sess is not None

        img = Image.open(io.BytesIO(image_bytes)).convert("RGB")
        W, H = img.size
        mask = np.frombuffer(mask_bytes, dtype=np.uint8).reshape(mh, mw)
        if (mw, mh) != (W, H):
            mask = np.asarray(
                Image.fromarray(mask, "L").resize((W, H), Image.NEAREST), dtype=np.uint8
            )

        # 裁一块包含整个洞的区域（带边距），减少 512 缩放带来的糊。
        crop_box = _crop_box(mask, W, H)
        cx0, cy0, cx1, cy1 = crop_box
        sub_img = img.crop(crop_box)
        sub_mask = mask[cy0:cy1, cx0:cx1]

        # 缩到模型尺寸
        sw, sh = sub_img.size
        sm_img = sub_img.resize((MODEL_SIZE, MODEL_SIZE), Image.LANCZOS)
        sm_mask = np.asarray(
            Image.fromarray(sub_mask, "L").resize((MODEL_SIZE, MODEL_SIZE), Image.NEAREST)
        )
        # 洞稍微扩 2px：模型对边界灰阶很敏感，扩一点能把角色边缘的杂色吃干净。
        sm_mask = _dilate(sm_mask > 127, 2)

        x = np.asarray(sm_img, dtype=np.float32) / 255.0
        x = np.transpose(x, (2, 0, 1))[None, ...]
        m = sm_mask.astype(np.float32)[None, None, ...]

        t0 = time.perf_counter()
        out = self._sess.run(None, {"image": x, "mask": m})[0]
        self._last_seconds = time.perf_counter() - t0
        self._calls += 1

        o = np.clip(np.asarray(out)[0], 0.0, 1.0)
        o = np.transpose(o, (1, 2, 0))
        out_img = Image.fromarray((o * 255.0).round().astype(np.uint8), "RGB")
        out_img = out_img.resize((sw, sh), Image.LANCZOS)

        # 贴回原图：洞内用补出来的，洞外保留原像素（避免整体被 512 缩放柔化）。
        full = img.copy()
        full.paste(out_img, (cx0, cy0))
        # 用羽化后的 mask 做合成，边界才不会有硬缝
        feather = Image.fromarray((sub_mask > 127).astype(np.uint8) * 255, "L")
        feather = feather.filter(ImageFilter.GaussianBlur(1.5))
        full.paste(out_img, (cx0, cy0), feather)

        return full.tobytes(), W, H


def _crop_box(mask: np.ndarray, W: int, H: int) -> tuple[int, int, int, int]:
    """按 mask 的包围盒取裁剪框（带边距，并保证是正方形以贴合 512 输入）。"""
    ys, xs = np.nonzero(mask > 127)
    if len(xs) == 0:
        return 0, 0, W, H
    x0, x1 = int(xs.min()), int(xs.max())
    y0, y1 = int(ys.min()), int(ys.max())
    bw, bh = x1 - x0 + 1, y1 - y0 + 1
    side = int(max(bw, bh) * (1.0 + 2.0 * CROP_MARGIN))
    side = max(side, 64)
    cx = (x0 + x1) // 2
    cy = (y0 + y1) // 2
    left = max(0, cx - side // 2)
    top = max(0, cy - side // 2)
    right = min(W, left + side)
    bottom = min(H, top + side)
    # 贴边时把窗口拉回图内
    left = max(0, right - side)
    top = max(0, bottom - side)
    return left, top, right, bottom


def _dilate(binary: np.ndarray, radius: int) -> np.ndarray:
    """二值膨胀（方形结构元），不引 scipy。"""
    out = binary.copy()
    for _ in range(radius):
        p = np.pad(out, 1, mode="constant", constant_values=False)
        out = (
            p[1:-1, 1:-1] | p[:-2, 1:-1] | p[2:, 1:-1] | p[1:-1, :-2] | p[1:-1, 2:]
        )
    return out


# PIL 的 GaussianBlur 要走 ImageFilter
from PIL import ImageFilter  # noqa: E402  (放这里避免顶部 import 顺序噪音)
