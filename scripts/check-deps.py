#!/usr/bin/env python
"""扫描 vendor 里的 DA3 源码，列出它真正 import 的第三方包，并报告缺失的。

用途：DA3 只用「最小依赖」vendor（不 pip install -e .，见 docs/selection.md §1.5），
所以必须知道它到底要什么。逐个 SModuleNotFoundError 地补太慢，这个脚本一次列全。

用法（用 illust3d 环境的 python）：
    python scripts/check-deps.py
    python scripts/check-deps.py --vendor sidecar/vendor/Depth-Anything-3/src

退出码：0 = 无缺失；1 = 有缺失（会打印建议的 pip 包名）。
"""
from __future__ import annotations

import argparse
import ast
import importlib.util
import pathlib
import sys

# 导入名 → pip 包名（只收本项目可能遇到的；其余默认同名）
PIP_NAME = {
    "cv2": "opencv-python",
    "PIL": "pillow",
    "yaml": "pyyaml",
    "skimage": "scikit-image",
    "sklearn": "scikit-learn",
    "torch": "torch",
    "torchvision": "torchvision",
    "huggingface_hub": "huggingface_hub",
    "safetensors": "safetensors",
    "omegaconf": "omegaconf",
    "imageio": "imageio",
    "addict": "addict",
    "plyfile": "plyfile",
    "trimesh": "trimesh",
    "open3d": "open3d",
    "moviepy": "moviepy",
    "pycolmap": "pycolmap",
    "evo": "evo",
    "e3nn": "e3nn",
    "gsplat": "gsplat",
    "typer": "typer",
    "jaxtyping": "jaxtyping",
    "bson": "pymongo",
    "einops": "einops",
    "numpy": "numpy",
    "scipy": "scipy",
    "matplotlib": "matplotlib",
    "tqdm": "tqdm",
    "requests": "requests",
    "fastapi": "fastapi",
    "uvicorn": "uvicorn",
    "xformers": "xformers",
}

# 上游把导出链改成懒加载后，这些包只在真正导出时才需要，属于「故意不装」
INTENTIONALLY_SKIPPED = {
    "trimesh", "open3d", "pycolmap", "evo", "e3nn", "moviepy", "gsplat",
    "xformers",  # swiglu_ffn 有 ImportError 回退
}


def collect_imports(root: pathlib.Path) -> set[str]:
    """收集 package 内所有模块级 import 的顶层模块名。"""
    mods: set[str] = set()
    for py in root.rglob("*.py"):
        try:
            tree = ast.parse(py.read_text(encoding="utf-8"))
        except (SyntaxError, UnicodeDecodeError):
            continue
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                for alias in node.names:
                    mods.add(alias.name.split(".")[0])
            elif isinstance(node, ast.ImportFrom):
                # level != 0 是包内相对导入，跳过
                if node.level == 0 and node.module:
                    mods.add(node.module.split(".")[0])
    return mods


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--vendor", default="sidecar/vendor/Depth-Anything-3/src")
    ap.add_argument("--package", default="depth_anything_3")
    args = ap.parse_args()

    root = pathlib.Path(args.vendor) / args.package
    if not root.is_dir():
        print(f"找不到 {root}（先跑 scripts/setup-env.ps1）")
        return 1

    std = set(sys.stdlib_module_names)
    local = {args.package}
    third = sorted(m for m in collect_imports(root) if m not in std and m not in local)

    installed, missing, skipped = [], [], []
    for m in third:
        if m in INTENTIONALLY_SKIPPED:
            skipped.append(m)
            continue
        try:
            ok = importlib.util.find_spec(m) is not None
        except (ImportError, ValueError):
            ok = False
        (installed if ok else missing).append(m)

    print(f"扫描: {root}")
    print(f"解释器: {sys.executable}")
    print()
    print(f"[已安装] {len(installed)}: {', '.join(installed) or '(无)'}")
    print(f"[故意跳过] {len(skipped)}: {', '.join(sorted(skipped)) or '(无)'}")
    print(f"    ^ 这些在上游只服务导出/注意力加速链，懒加载补丁 + ImportError 回退已覆盖")
    print()

    if missing:
        pkgs = sorted({PIP_NAME.get(m, m) for m in missing})
        print(f"[缺失] {len(missing)}: {', '.join(missing)}")
        print()
        print("安装命令：")
        print("  pip install " + " ".join(f'"{p}"' for p in pkgs))
        return 1

    print("[缺失] 无 —— 依赖已齐")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
