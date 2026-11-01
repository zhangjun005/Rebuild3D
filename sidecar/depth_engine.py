#!/usr/bin/env python
"""DA3 单目深度推理引擎的封装。

只做一件事：图片字节 → 深度图。不碰几何、不碰存储、不碰协议（那是 server.py 的事）。
模型常驻显存，提供显式卸载。

设计约束（见 docs/selection.md、docs/go-scope.md）：
  - DA3 源码是 vendor 的，不在 site-packages，导入前要手动加 sys.path
  - 不装 evo / plyfile / trimesh / open3d 等（已打懒加载补丁，见 sidecar/vendor/PATCHES.md）
  - 输出不带 conf / intrinsics（DA3MONO 特性，已实测确认，见 kron 假设 da3mono-no-confidence-map）
  - 峰值显存实测 2157 MiB（见 kron 假设 da3-mono-fits-8gb）
"""
from __future__ import annotations

import io
import os
import sys
import time
from pathlib import Path
from typing import Optional

# ── 缓存位置兜底：即使没走 activate.d，也别让 HuggingFace 落 C 盘 ──────────
os.environ.setdefault("HF_HOME", r"E:\models\hf")
os.environ.setdefault("HF_ENDPOINT", "https://hf-mirror.com")
os.environ.setdefault("TORCH_HOME", r"E:\models\torch")
# 实测 hf-mirror 会把请求重定向到 xet-bridge CDN；xet 通道曾经卡死过。
# 但 2026-11-01 那次重试 xet 反而正常，所以这里不禁用，只在必要时由外部设置。
os.environ.setdefault("PYTHONNOUSERSITE", "1")

REPO_ROOT = Path(__file__).resolve().parent.parent
VENDOR_SRC = REPO_ROOT / "sidecar" / "vendor" / "Depth-Anything-3" / "src"

DEFAULT_MODEL = "depth-anything/DA3MONO-LARGE"
DEFAULT_RES = 504


def _log(msg: str) -> None:
    """日志一律走 stderr —— stdout 是帧协议的专线，不能污染。"""
    print(f"[depth-engine] {msg}", file=sys.stderr, flush=True)


class DepthEngine:
    """DA3MONO 的薄封装。线程不安全，调用方负责串行化（server.py 是单线程循环）。"""

    def __init__(
        self,
        model: str = DEFAULT_MODEL,
        device: str = "cuda",
        process_res: int = DEFAULT_RES,
        local_files_only: bool = True,
    ) -> None:
        self.model_id = model
        self.device = device
        self.process_res = process_res
        self.local_files_only = local_files_only
        self._model = None
        self._torch = None
        self._load_seconds: Optional[float] = None
        self._last_infer_seconds: Optional[float] = None

    # ── 加载 ────────────────────────────────────────────────────────────────
    def load(self) -> None:
        if self._model is not None:
            return

        if not VENDOR_SRC.is_dir():
            raise RuntimeError(
                f"找不到 DA3 源码：{VENDOR_SRC}\n"
                "先跑 scripts/setup-env.ps1"
            )
        if str(VENDOR_SRC) not in sys.path:
            sys.path.insert(0, str(VENDOR_SRC))

        t0 = time.time()
        import torch  # noqa: PLC0415  (延迟导入：PING 之类的轻请求不该等 torch)

        self._torch = torch

        if self.device == "cuda" and not torch.cuda.is_available():
            _log("CUDA 不可用，退回 CPU（会非常慢）")
            self.device = "cpu"

        # 优先用本地缓存快照路径，避免运行时联网；找不到再退回 repo id
        target = self.model_id
        if self.local_files_only:
            try:
                from huggingface_hub import snapshot_download  # noqa: PLC0415

                target = snapshot_download(self.model_id, local_files_only=True)
                _log(f"用本地快照: {target}")
            except Exception as e:  # noqa: BLE001
                _log(f"本地快照不可用（{type(e).__name__}），回退到 repo id: {self.model_id}")

        from depth_anything_3.api import DepthAnything3  # noqa: PLC0415

        self._model = DepthAnything3.from_pretrained(target)
        self._model = self._model.to(self.device)
        self._model.eval()

        self._load_seconds = time.time() - t0
        vram = self.vram_used_mb()
        _log(
            f"模型已加载 {self.model_id} device={self.device} "
            f"耗时 {self._load_seconds:.1f}s 显存 {vram:.0f} MiB"
        )

    # ── 推理 ────────────────────────────────────────────────────────────────
    def infer(self, image_bytes: bytes) -> tuple[bytes, int, int, int]:
        """图片字节 → (depth 原始 float32 字节, width, height, flags)。

        flags bit0 = 是否附带 conf。DA3MONO 不带，所以恒为 0。
        返回裸字节而不是 numpy，避免把 numpy 类型泄漏到协议层。
        """
        self.load()
        assert self._model is not None and self._torch is not None

        from PIL import Image  # noqa: PLC0415

        import numpy as np  # noqa: PLC0415

        try:
            img = Image.open(io.BytesIO(image_bytes)).convert("RGB")
        except Exception as e:  # noqa: BLE001
            raise ValueError(f"图片无法解码: {e}") from e

        t0 = time.time()
        with self._torch.inference_mode():
            pred = self._model.inference([img], process_res=self.process_res)
        self._last_infer_seconds = time.time() - t0

        depth = np.asarray(pred.depth)
        if depth.ndim == 3:  # (N, H, W) → (H, W)
            depth = depth[0]
        depth = np.ascontiguousarray(depth, dtype=np.float32)

        # NA/Inf 清零：下游按「无效深度」处理（Probe 里实测 nan=0%，防御而已）
        np.nan_to_num(depth, copy=False, nan=0.0, posinf=0.0, neginf=0.0)

        h, w = depth.shape
        return depth.tobytes(), w, h, 0

    # ── 状态 / 卸载 ─────────────────────────────────────────────────────────
    def vram_used_mb(self) -> float:
        if self._torch is None or self.device != "cuda":
            return 0.0
        try:
            return self._torch.cuda.memory_allocated() / 2**20
        except Exception:  # noqa: BLE001
            return 0.0

    def vram_peak_mb(self) -> float:
        if self._torch is None or self.device != "cuda":
            return 0.0
        try:
            return self._torch.cuda.max_memory_allocated() / 2**20
        except Exception:  # noqa: BLE001
            return 0.0

    def status(self) -> dict:
        return {
            "model": self.model_id,
            "device": self.device,
            "ready": self._model is not None,
            "process_res": self.process_res,
            "vram_used_mb": round(self.vram_used_mb(), 1),
            "vram_peak_mb": round(self.vram_peak_mb(), 1),
            "load_seconds": None if self._load_seconds is None else round(self._load_seconds, 2),
            "last_infer_seconds": (
                None if self._last_infer_seconds is None else round(self._last_infer_seconds, 3)
            ),
        }

    def unload(self) -> None:
        if self._model is None:
            return
        self._model = None
        if self._torch is not None and self.device == "cuda":
            self._torch.cuda.empty_cache()
        _log("模型已卸载，显存已释放")
