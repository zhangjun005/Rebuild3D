# 选型与规范

<!-- @kron:intent geometry/projection/orthographic -->
<!-- @kron:intent process/contract-flip-one-side -->

> 状态：待确认（v1 开工前定稿）
> 适用范围：插画 → 点云 → 风格渲染 主线（见 [scope.md](scope.md)）
> **本文是施工依据。任何与技术选型冲突的临时决定，以本文为准或先改本文。**

---

## 0. 三条铁律

| # | 铁律 | 原因 |
|---|---|---|
| **1** | **只在 conda env 里装东西，绝不碰全局 / base** | 本机 base 是 Python 3.13.5，被 DSH、其他工具共用。污染 = 全线崩 |
| **2** | **所有缓存与模型都落在 E 盘** | **C 盘只剩 3.9 GB**。pip 默认缓存在 `C:\Users\<用户名>\AppData\Local\pip\Cache`，第一个模型就能塞满 |
| **3** | **不引用 old 工程代码作为依赖** | `code/` 目录已丢失依赖，仅作参考（见 [archive/legacy-inventory.md](archive/legacy-inventory.md)） |

---

## 1. 环境：conda 隔离

### 1.1 本机现状（已核实）

| 项 | 值 | 判断 |
|---|---|---|
| conda | 25.5.1 @ `E:\CondaBody` | ✅ |
| `envs_dirs` | **`E:\conda\envs_dirs`**（第一优先级） | ✅ 新 env 自动落 E 盘 |
| `pkgs_dirs` | **`E:\conda\pkg_dirs`** | ✅ |
| channels | 清华 TUNA 镜像 | ✅ 国内速度快 |
| base python | 3.13.5 | ⚠️ **不碰** |
| **pip 缓存** | **无全局配置 → 默认在 C 盘** | ❌ **必须重定向** |
| 磁盘 | C: **3.9 GB** / E: 110.3 GB | ⚠️ |
| GPU | RTX 4060 Laptop **8 GB** | ✅ |
| Go | go1.27.1，`GOPROXY=https://goproxy.cn` | ✅ |
| Node | v22.23.3 / npm 10.9.9 | ✅ |

### 1.2 创建环境

**环境名：`illust3d`，Python 3.10**（与已验证可用的 `3d-build` 同版本；DA3 的 `requires-python = ">=3.9, <=3.13"`）

```powershell
# 1) 建环境（.condarc 已把 envs_dirs 指向 E 盘，不用额外指定路径）
conda create -n illust3d python=3.10 -y

# 2) 把环境专属的环境变量写进 activate.d，激活即生效
#    目录：E:\conda\envs_dirs\illust3d\etc\conda\activate.d\
#    原因：不想每次开 shell 都手动 export，也不想写进系统环境变量
```

`activate.d\env_vars.ps1`（PowerShell 用）与 `env_vars.bat`（cmd 用）内容相同：

```
PIP_CACHE_DIR    = E:\cache\pip          # pip 缓存离开 C 盘
PYTHONNOUSERSITE = 1                     # ★隔离：不读 C:\Users\<用户名>\AppData\Roaming\Python 下的包
TMP / TEMP / TMPDIR = E:\cache\tmp       # ★临时目录也必须离开 C 盘（见下方踩坑）
HF_HOME          = E:\models\hf          # HuggingFace 模型与缓存
HF_ENDPOINT      = https://hf-mirror.com # 国内镜像
TORCH_HOME       = E:\models\torch       # torch hub 缓存
```

**三条是"不污染全局 / 不占 C 盘"的核心保障：**

| 变量 | 作用 |
|---|---|
| `PYTHONNOUSERSITE=1` | 不会意外 import 到 `%APPDATA%\Python` 里的用户级包（另一个隐性污染源） |
| `PIP_CACHE_DIR` | pip 缓存不进 C 盘 |
| `TMP` / `TEMP` / `TMPDIR` | **pip 的临时解包目录、HF 下载暂存都走这里** |

> ### 踩坑一：`PIP_REQUIRE_VIRTUALENV` 和 conda 不兼容（2026-11 实测）
>
> 原本还打算设 `PIP_REQUIRE_VIRTUALENV=1` 当"硬闸"——手滑在 base 里 `pip install` 会直接拒绝。
> **但它会让 env 自己的 pip 也拒绝工作**，报 `Could not find an activated virtualenv (required)`。
> 原因：pip 只把 venv 认作虚拟环境，而 conda env 里 `sys.prefix == sys.base_prefix`，pip 判定"不在虚拟环境里"。
>
> **改用两条更实在的保障：**
> 1. **永远显式调用 env 解释器的绝对路径**：`E:\conda\envs_dirs\illust3d\python.exe -m pip`，不用裸 `pip` / `python`
> 2. `scripts/setup-env.ps1` 开头断言 `sys.prefix` 必须落在 `E:\conda\envs_dirs` 下，否则直接中止
>
> 教训：**"硬闸"要选在真正能生效的层**。隔离靠的是"永远不碰 base 的解释器"，不是靠一个开关变量。
>
> ### 踩坑二：只重定向 `PIP_CACHE_DIR` 不够，`TEMP` 才是大头（2026-11 实测）
>
> 第一次装 torch 时只设了 `PIP_CACHE_DIR`，结果 pip 把 2.4 GB 的轮子先解到
> `C:\Users\<用户名>\AppData\Local\Temp\pip-unpack-*\`，**C 盘从 3.8 GB 被吃到 1.11 GB**，任务被迫中止。
>
> `PIP_CACHE_DIR` 管的是"装完之后留存的缓存"；**下载过程中的临时文件走 `%TEMP%`，是另一条路径**。
> 现在 `TMP`/`TEMP`/`TMPDIR` 一并指向 `E:\cache\tmp`，并在脚本开头加了 C 盘空间护栏。
>
> 教训：**"所有缓存落 E 盘"要数清有几条路径**——cache / temp / home / hub，漏一条就前功尽弃。

### 1.3 装依赖：最小起步，缺什么补什么

**不要一次性装一大坨。** 先装下面 10 个，跑起来；报 `ModuleNotFoundError: X` 就把 X 加进清单，再重新生成 requirements。

```powershell
conda activate illust3d

# torch：与本机已验证的 3d-build 完全同版本，零新风险
pip install torch==2.5.1 torchvision==0.20.1 --index-url https://download.pytorch.org/whl/cu121

# DA3 推理所需的最小集（见 1.4 说明）
pip install "numpy<2" opencv-python pillow huggingface_hub safetensors einops omegaconf
```

`sidecar/requirements.txt` 记录上面这一份，并**锁定版本**，保证可重建。

### 1.4 明确不装的东西，以及为什么

| 不装 | 原因 |
|---|---|
| `xformers` | DA3 的 `model/dinov2/layers/swiglu_ffn.py` 里是 `try: from xformers.ops import SwiGLU / except ImportError:` 有纯 PyTorch 回退。Windows 上装 xformers 有把 torch 反向改版本的风险 |
| `trimesh` / `open3d` / `pycolmap` / `evo` / `e3nn` / `moviepy` / `gsplat` | 这些是 DA3 `pyproject.toml` 的依赖，但**只服务导出功能**。原因：`api.py` 顶层写了 `from depth_anything_3.utils.export import export`，而 `utils/export/__init__.py` 又顶层 import 了 gs/colmap/glb/feat_vis → 只做深度推理也会被拖入整条导出链。**做法见 1.5** |
| `diffusers` / `transformers` / `accelerate` | 旧项目的 SD 栈，本项目完全不用 |
| `open3d` | 点云几何在 Go 侧自己写（见 [go-scope.md](go-scope.md)），不需要 |
| FastAPI / uvicorn | sidecar 用 Python **标准库** `http.server` 就够（见 §4.1），零框架依赖 |

### 1.5 DA3 源码：vendor 而不是 pip install

```powershell
# 固定 commit，不跟 main 漂
git clone --depth 1 https://github.com/ByteDance-Seed/Depth-Anything-3 sidecar\vendor\Depth-Anything-3
cd sidecar\vendor\Depth-Anything-3
git rev-parse HEAD   # ← 记进 sidecar/VENDOR_COMMIT.txt
```

**绝不用 `pip install -e .`**（会把 §1.4 那一串全拉进来）。

**必须打的一个本地补丁**（一行改动，改完记在 `sidecar/vendor/PATCHES.md`）：

```python
# sidecar/vendor/Depth-Anything-3/src/depth_anything_3/api.py
# 原（约第 30 行）：from depth_anything_3.utils.export import export
# 改为：删除这一行
# 然后在 _export_results() 方法内部第一行加：
#     from depth_anything_3.utils.export import export   # PATCH: 懒加载，避免拖入 gs/colmap/glb 依赖
```

sidecar 启动时把 `sidecar/vendor/Depth-Anything-3/src` 加进 `sys.path` 即可 import。

> **升级 DA3 时**：重新 clone → 重新打这个补丁 → 更新 `VENDOR_COMMIT.txt`。补丁只有一处，不会失控。

### 1.6 环境自检（装完立刻跑）

```powershell
conda activate illust3d
python -c "import sys, torch; print(sys.prefix); print(torch.__version__, torch.cuda.is_available(), torch.cuda.get_device_name(0))"
python -c "import sys; print('user-site:', sys.path)"   # 确认没有 AppData\Roaming\Python
pip -V                                                   # 确认 pip 属于 illust3d
```

通过标准：`sys.prefix` 指向 `E:\conda\envs_dirs\illust3d`，`cuda_available=True`，路径里没有 `C:\Users\<用户名>\AppData\Roaming`。

---

## 2. 模型选型

**单图插画场景下，多视图优势用不上**，所以不走 DA3 主系列。

| 优先级 | 模型 (HF repo) | 参数 | 许可证 | 显存估算 | 用途 |
|---|---|---|---|---|---|
| **1** | `depth-anything/DA3MONO-LARGE` | 0.35B | **Apache-2.0** | ~1.4 GB (fp32) | 主力：专做单目相对深度，直接预测 depth 而非 disparity |
| 2 | `depth-anything/DA3-SMALL` | 0.08B | **Apache-2.0** | ~0.32 GB | 快速验证 / 低配降级 |
| 3 | `depth-anything/DA3METRIC-LARGE` | 0.35B | **Apache-2.0** | ~1.4 GB | 备选：**带 sky segmentation**，可用来抠背景 |

**明确不用**：`DA3-LARGE` / `DA3-GIANT` / `DA3NESTED-*` —— 装不下（1.15B/1.40B）且 **CC BY-NC 4.0 非商用**。

**下载位置**：`HF_HOME=E:\models\hf`，磁盘占用预估 1.4 GB（主）+ 0.32 GB（备）。

### 2.1 必须先验证的一件事（不能假设）

> 官方文档只写 `conf ... optional, depends on model`。**`DA3MONO-LARGE` 的 `Prediction` 是否带 `conf` 必须先跑一次确认。**

- 有 `conf` → 直接用于飞点过滤
- 没有 → 退化为"深度梯度 + 边缘检测"过滤（[scope.md](scope.md) §5 已列为第 2 优先验证项）

---

## 3. 仓库结构

```
Rebuild3D/
  cmd/illust3d/main.go           Go 入口（单二进制）
  internal/
    api/                         路由、SSE、静态资源
    infer/                       sidecar 客户端（二进制协议解码）
    geometry/                    反投影、边缘剔除、重采样、法线
    cloud/                       点云内部表示 + 二进制编解码
    exporters/                   PLY（后续 GIF/WebM）
    config/
  sidecar/
    server.py                    常驻进程（stdlib http.server）
    depth_engine.py              DA3 封装（懒加载 + 卸载）
    requirements.txt
    VENDOR_COMMIT.txt
    vendor/
      Depth-Anything-3/          git clone，固定 commit，已打补丁
      PATCHES.md                 补丁记录
  web/                           three.js
    src/
      styles/*.glsl              每种风格一个 shader（便于迭代）
      viewercore.ts
  scripts/
    setup-env.ps1                建 env + 写 activate.d + 装依赖
  docs/
```

---

## 4. 接口规范

### 4.1 Go ↔ Python sidecar：stdio 帧协议

**形态**：Python 是 Go 用 `os/exec` 拉起的**常驻子进程**。走 stdin/stdout 管道，**不开端口、不走网络**。

| 项 | 决定 |
|---|---|
| 传输 | Go `os/exec` 的 stdin/stdout 管道 ↔ Python `sys.stdin.buffer` / `sys.stdout.buffer` |
| 日志 | sidecar 日志**必须写 stderr**，stdout 只放帧；否则污染流 |
| 协议 | 每帧 = 4 字节长度前缀 + 定长头 + 变长体，全小端 |

**为什么不用 HTTP**（初版设计写的是 localhost HTTP，后来改掉）：

| | **stdio** | HTTP |
|---|---|---|
| 端口 | 不需要 | 要管端口占用与冲突 |
| 生命周期 | 子进程，父死子随 | **孤儿进程会继续占着 2 GB 显存** |
| 网络暴露 | 完全没有 | 要记得只绑 127.0.0.1 |
| 依赖 | 两侧都是标准库 | 两侧都要一个 web server |
| 手工调试 | 差一些 | `curl` 方便 |
| 将来远程部署 | 要换传输层 | 天然支持 |

单机、单用户、单 GPU 的工具，前四项压倒后两项。**协议是消息级的，将来真要换成 HTTP/gRPC，只换传输层，帧定义不动。**

**请求帧**（Go → Python）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `total_len` | uint32 | 本帧后续字节数 |
| `cmd` | uint8 | 1=PING 2=INFER 3=UNLOAD |
| `arg` | uint32 | INFER 时为 `process_res`，0 表示用默认 |
| `body` | bytes | INFER 时是**原始图片字节**（不重新编码） |

**响应帧**（Python → Go）：**同样带 4 字节长度前缀**，结构 = `total_len(u32) + status(u8) + body`

| `cmd` | `status=0` 时的 body |
|---|---|
| PING | UTF-8 JSON：`{model, device, vram_used_mb, ready}` |
| INFER | `width` / `height` / `flags`（uint32×3）+ `depth` float32[W*H] + `conf` float32[W*H]（仅 `flags&1` 时存在） |
| UNLOAD | 空 |
| 任意 | `status=1` 时 body = UTF-8 错误信息；`total_len = 1 + len(body)` |

**为什么不用 JSON/base64**：一张 504×378 的 float32 深度图约 0.76 MB，base64 后 1.0 MB 且要两次解析。二进制一行代码搞定。

**超时**：模型加载放在启动时的 PING/warmup 阶段（30~120 s）；之后每帧 INFER 超时 60 s。sidecar 崩溃 = 管道 EOF，Go 侧按事件上报并重启一次。

### 4.2 Go ↔ 浏览器（点云）

**内部传输格式**（零解析，JS 直接 `new Float32Array(buf, offset, n)`）：

```
magic   "ILC1"        4 B
count   uint32        4 B
pos     float32[3*count]
col     u8[3*count]
```

元数据（点数、包围盒、参数、风格预设）走独立 JSON：`GET /api/cloud/{id}/meta`。

**对外导出格式**：`PLY`（`binary_little_endian`），给 Blender/MeshLab 等外部工具。**内部传输和对外导出是两套格式，不要合并。**

### 4.3 渲染在哪里

> **渲染只在浏览器。Go 和 Python 都不做渲染。**

理由：风格化是 shader 的核心价值，服务端渲染需要 headless 浏览器（重）。v1 的动图导出用浏览器内置 `MediaRecorder` 录 WebM（零依赖），GIF 留到需要时再加。

### 4.4 反投影的相机约定（必须三端一致）

§4.1 只定义了协议，没定义几何。若 Go 反投影和前端/文档对不上，点云会镜像或上下颠倒。约定如下：

| 项 | 约定 |
|---|---|
| 坐标系 | `+x` 右 / `+y` 上 / **`+z` 指向观察者**（OpenGL 习惯，右手系） |
| **深度语义** | **`d` 是距离，d 越大越远**（DA3MONO 实测：主体 d≈0.48，背景 d≈0.86） |
| **相机位置** | 站在 **`+Z`** 上朝 **`-Z`** 看（three.js 默认朝向） |
| 像素中心 | 取 `(u+0.5, v+0.5)`，避免半像素偏移 |
| 透视反投影 | `x = (u + 0.5 - cx) * d / fx`，`y = (cy - v - 0.5) * d / fy`，**`z = -d`** |
| 正交反投影 | `x = (u + 0.5 - cx) * scale`，`y = (cy - v - 0.5) * scale`，**`z = -d * scale`** |
| 无效深度 | 输出零值点并**保持逐像素 1:1 下标**（不要压缩数组，过滤交给 `FilterDepthEdges`） |
| `FilterDepthEdges` | 返回**丢弃掩码**（`true` = 丢弃），阈值为绝对深度差，跳变正负两侧都标记 |
| `VoxelDownsample` | 代表点取格内位置/颜色均值，输出按格坐标字典序 → 与并行度无关、逐位可复现 |
| 法线 | PCA 固有朝向**未统一**，渲染侧需按视线翻转 |

> ### ⚠️ `z` 的符号曾经写错过，不要改回去（2026-11-01 实测）
>
> 早期实现写成 `z = d`（`d` 是距离，正值），而相机按「+z 朝观察者」放在 `+Z` 看 `-Z`。
> 两者叠加导致**整个点云模式远近关系反向**——背景被当成近景、主体被当成远景。
>
> 一行错误同时污染了三条判断，当时都被误当成「点云模式天生不行」：
> 1. **空气透视把主体洗白**（雾加在了主体上）
> 2. **按深度上墨落到背景**
> 3. **视差方向相反**（背景比主体动得多）
>
> **两边只能有一边翻。** 现在的约定是 Go 输出 `z = -d`（数据本身符合文档，
> PLY 导出给外部工具时方向也才对），渲染层用标准 `+Z → -Z` 相机。
> 若哪天在管线里改符号，**必须同步改渲染层的相机方向，反之亦然**。

### 4.5 端口

**默认 `127.0.0.1:18123`**（可用 `-addr` 覆盖）。

原计划用 8123，但本机 `0.0.0.0:8123` 与 `127.0.0.1:8123` 被无关系统进程 **ParkCloud** 占用，
绑定会失败。18123 已实测可用。这也是"默认端口必须可覆盖"的原因——不要把它写死在代码里。

---

## 5. 编码规范（简要）

| 项 | 规范 |
|---|---|
| Go | 标准库 `net/http` + `ServeMux`（Go 1.22+ 够用，不引 chi）；`go:embed` 打包 `web/dist`；geometry 必须有表驱动单测；不引入 ORM/DI 框架 |
| Python sidecar | **单文件优先，≤300 行**；只做推理，不碰几何/存储/HTTP 业务；模型常驻，提供显式卸载 |
| Web | shader 独立成 `.glsl` 文件（`?raw` 导入），不要把 GLSL 塞进 TS 字符串；每种风格一个文件 |
| 配置 | 一个 `internal/config` + `config.toml`；不散落 magic number |
| 文档 | 改选型就改本文，不写到聊天记录里 |

---

## 6. 资源预算

| 资源 | 预算 | 说明 |
|---|---|---|
| 显存 | < 2.5 GB（目标 < 6 GB 绝对上限） | DA3MONO 0.35B @ 504px 单图 |
| conda env 磁盘 | ~3.5 GB | torch cu121 占大头 |
| 模型磁盘 | ~1.8 GB | 主 + 备 |
| **C 盘新增** | **0** | 全部重定向到 E 盘 |
| 端到端耗时 | < 5 s（预热后） | 单图 504px |
| 点云点数 | 10 万 ~ 50 万 | 浏览器 60fps 上限；这也是美术参数，见 [rendering-styles.md](rendering-styles.md) |
