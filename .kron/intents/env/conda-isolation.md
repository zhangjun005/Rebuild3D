<!-- kron:frontmatter -->
symbol:
    - illust3d
created_by: '@zhangjun005'
updated_at: 2026-11-01T08:47:59.5222516Z
status: active
<!-- /kron:frontmatter -->
# 环境隔离：只用 conda env

> > 单独 conda env `illust3d`（Python 3.10），绝不向 base / 全局 Python 安装；所有缓存与模型落 E 盘。


## 为什么（Why）

两个硬约束：

1. **base 不能动。** 本机 base 是 Python 3.13.5，被 DSH 和其他工具共用。往里装 torch 会让别的工具全线崩。
2. **C 盘只剩 3.9 GB。** 而 pip 的默认缓存在 `C:\Users\<用户名>\AppData\Local\pip\Cache`，
   HuggingFace 默认在 `C:\Users\<用户名>\.cache\huggingface`。下载第一个模型就可能把 C 盘塞满。

好消息是 conda 侧已经是对的：`.condarc` 里 `envs_dirs=E:\conda\envs_dirs`、`pkgs_dirs=E:\conda\pkg_dirs`，
新环境天然落在 E 盘。要补的只有 pip 和 HF 的缓存位置。

## 如何（How）

三重保障，写进 `E:\conda\envs_dirs\illust3d\etc\conda\activate.d\`，激活环境即生效，不改系统环境变量：

| 变量 | 作用 |
|---|---|
| `PIP_REQUIRE_VIRTUALENV=1` | **硬闸**：手滑在 base 里敲 `pip install` 会直接报错拒绝，而不是默默装进去 |
| `PYTHONNOUSERSITE=1` | 不读 `%APPDATA%\Python` 下的用户级包——另一个隐性污染源 |
| `PIP_CACHE_DIR` / `HF_HOME` / `TORCH_HOME` | 全部重定向到 E 盘 |

外加两条安装纪律：**不装 xformers**、**不 `pip install -e .`**（详见 `docs/selection.md` §1.4）。

## 权衡（Tradeoffs）

- 每次开工要先 `conda activate illust3d`
- 环境不能直接拷到别的机器，要靠 `requirements.txt` + `scripts/setup-env.ps1` 重建
- 没有用 Docker：Windows 上 GPU 直通要 WSL2 + 额外配置，对这个规模的项目不划算

## 备选（Alternatives）

- **venv**：轻，但不方便管理 CUDA 版 torch 的轮子来源
- **Docker**：干净，但 Windows GPU 直通麻烦，且失去直接调试的便利
