"""动漫角色分割引擎（anime-seg / isnet_is）。

用途：给点云管线提供「角色 / 背景」语义分类，进而计算边界带。
模型：skytnt/anime-seg（Apache-2.0），ONNX 版本，onnxruntime CPU 推理。

为什么用 CPU：单张 1024 输入约 0.6~0.7s，够用；不占显存，不和 DA3 抢 GPU。

为什么不用 torch 加载 safetensors：那个仓库不带模型定义代码，要额外 vendor ISNet
架构；ONNX 自包含，依赖更小（onnxruntime 约 40MB，无 CUDA 依赖）。

失败模式（实测）：
  - 浅色角色 + 浅色背景 → 整张图被判为角色（覆盖率>90%）。调用方应检测并退化。
"""

from __future__ import annotations

import io
import os
from pathlib import Path

import numpy as np
from PIL import Image

DEFAULT_SEG_MODEL = os.environ.get(
    "ILLUST3D_SEG_MODEL", r"E:\models\anime-seg\isnetis.onnx"
)

# 与 anime-seg 仓库一致的 ImageNet 归一化。
_MEAN = np.array([0.485, 0.456, 0.406], dtype=np.float32)
_STD = np.array([0.229, 0.224, 0.225], dtype=np.float32)


class SegEngine:
    """懒加载的角色分割引擎。线程不安全，由 server 的单线程循环保证串行。"""

    def __init__(self, model_path: str = DEFAULT_SEG_MODEL, enabled: bool = True) -> None:
        self.model_path = Path(model_path)
        self.enabled = enabled and self.model_path.exists()
        self._sess = None
        self._in_name: str | None = None
        self._size = 1024
        self._calls = 0
        self._last_seconds = 0.0

    # ── 生命周期 ──────────────────────────────────────────────────────────
    def _ensure(self) -> None:
        if self._sess is not None:
            return
        if not self.enabled:
            raise RuntimeError(
                f"分割模型不可用（enabled={self.enabled}, path={self.model_path}）"
            )
        import onnxruntime as ort  # 延迟导入：不用分割时不占启动时间

        so = ort.SessionOptions()
        so.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
        # 0 = 交给 runtime 自己决定（用满物理核）
        so.intra_op_num_threads = 0
        self._sess = ort.InferenceSession(
            str(self.model_path), sess_options=so, providers=["CPUExecutionProvider"]
        )
        inp = self._sess.get_inputs()[0]
        self._in_name = inp.name
        if isinstance(inp.shape[2], int):
            self._size = int(inp.shape[2])
        elif isinstance(inp.shape[3], int):
            self._size = int(inp.shape[3])

    def status(self) -> dict:
        return {
            "enabled": bool(self.enabled),
            "loaded": self._sess is not None,
            "model": self.model_path.name,
            "input_size": self._size,
            "calls": self._calls,
            "last_seconds": round(self._last_seconds, 3),
        }

    def unload(self) -> None:
        self._sess = None

    # ── 推理 ──────────────────────────────────────────────────────────────
    def segment(self, image_bytes: bytes) -> tuple[bytes, int, int]:
        """返回 (mask u8[W*H], W, H)，mask 里 255=角色、0=背景、128=不确定带。

        输出尺寸 = 输入图片的原始尺寸（便于和深度图对齐）。
        """
        import time

        self._ensure()
        assert self._sess is not None and self._in_name is not None

        img = Image.open(io.BytesIO(image_bytes))
        img.load()
        rgb = img.convert("RGB")
        w, h = rgb.size

        small = rgb.resize((self._size, self._size), Image.BILINEAR)
        x = np.asarray(small, dtype=np.float32) / 255.0
        x = (x - _MEAN) / _STD
        x = np.transpose(x, (2, 0, 1))[None, ...].astype(np.float32)

        t0 = time.perf_counter()
        out = self._sess.run(None, {self._in_name: x})[0]
        self._last_seconds = time.perf_counter() - t0
        self._calls += 1

        logits = np.asarray(out, dtype=np.float32).reshape(self._size, self._size)
        prov = 1.0 / (1.0 + np.exp(-logits))

        # 用双线性把概率图缩回原尺寸，再按阈值切成三档：
        #   >= 0.6 → 角色(255)   <= 0.4 → 背景(0)   中间 → 不确定(128)
        # 三档比二值更有用：调用方可以把"不确定"当作天然的边界带起点。
        pm = Image.fromarray((prov * 255.0).astype(np.uint8), "L").resize((w, h), Image.BILINEAR)
        p = np.asarray(pm, dtype=np.uint8)

        mask = np.zeros((h, w), dtype=np.uint8)
        mask[p >= 153] = 255   # 0.6 * 255 = 153
        mask[(p > 102) & (p < 153)] = 128  # 0.4~0.6 → 不确定
        # p <= 102 保持 0

        return mask.tobytes(), w, h
