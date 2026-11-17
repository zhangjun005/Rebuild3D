# Rebuild3D 现状盘点与重构方案

> 盘点时间：基于仓库现有内容 + Depth Anything 3 (DA3) 上游源码/文档核实
> 结论性质：只做诊断与方案，未改动任何代码

---

## 0. 先说结论

1. **这份代码现在跑不起来**，不是"老旧"，是"缺件"：深度估计的核心模块 `newServer/assets/depth/*` 根本不在仓库里，点云接口会静默退化成 `np.random.rand()` 随机噪声。
2. **体积压力 99.5% 来自 IOPaint**：446.8 MB / 28179 个文件里，`IOPaint-main` 占 444.7 MB / 28096 个文件。你自己的代码只有 11 个 py 文件（2511 行）+ 32 个前端文件（6609 行）。
3. **SD 模型这条线可以整条砍掉，且不伤主线**。你想丢的那些功能（AI 修图/物体移除/文本掩码）恰好是"模型最重、和 3D 关系最弱"的部分。主线只有一件事：**图 → 深度 → 点云 → 浏览器里能转能走进去看**。
4. **换成 DA3 不是版本升级，是能力换代**。旧管线要手工猜焦距（写死 `fx=fy=529.5`）和手工截断深度（写死 `max_depth=20`）；DA3 一次前向直接给出 `depth + confidence + 相机内参 K + 相机外参 E`。这意味着两件新事：**不用猜相机**、**多张照片能拼成一个统一世界坐标系的点云**。
5. **8GB 显存的正确选择是 DA3-BASE（0.12B, Apache-2.0）或 DA3-LARGE（0.35B, 但 CC BY-NC 4.0 非商用）**。DA3-GIANT / NESTED 系列（1.15B / 1.40B）在 4060 Laptop 8GB 上不要碰。

---

## 1. 盘点：这个项目实际是什么

### 1.1 体量

| 部分 | 文件数 | 行数 / 体积 | 说明 |
|---|---|---|---|
| `code/backend` | 11 py | 2511 行 | FastAPI，3 个路由模块 |
| `code/frontend/src` | 32 | 6609 行 | React 18 + TS + three.js，6 个页面 |
| `code/IOPaint-main` | 28096 | **444.7 MB** | 上游仓库整包 vendored，非本项目代码 |
| 合计 | 28179 | 446.8 MB | 99.5% 体积是 IOPaint |

无 `.git`、无 `node_modules`、无任何模型权重。根目录另有一个 `code.7z`（75 MB，估计就是这个 `code/` 的压缩备份）。

### 1.2 原设计的一条流

```
上传图 → [GroundingDINO + SAM2 文本掩码] → [IOPaint 修复/移除/插入]
       → [DA2 单目深度] → [手写反投影 + open3d 增强] → three.js 点云查看
```

两个进程：FastAPI(8000) + IOPaint 独立服务(8080)，前端 `api.ts` 同时打两边。

### 1.3 三个致命断点（按严重度）

**断点 A：深度/点云模块整块缺失 → 静默返回随机噪声**
- 加载逻辑：`backend/api/routes/depth_estimation.py:22-39`、`backend/api/routes/point_cloud.py:22-54`
- 期望路径：`../newServer/assets/depth/depth_estimation.py`、`enhanced_point_cloud_processor.py`
- 实际情况：`E:\works\Rebuild3D\newServer` **不存在**，`assets_path` 判断失败 → `DepthEstimationModule = None`
- 后果：`point_cloud.py:166` 和 `:178` 直接 `depth_array = np.random.rand(h, w) * 10.0`，即点云接口"成功"返回一团噪声。这是最坏的一种失败——不报错。

**断点 B：前后端接口契约对不上**
- 前端调用 `api.ts:116/134/156/301` → `/api/process/text-mask`、`/api/process/depth-estimation`、`/api/process/inpaint`、`/api/process/full-pipeline`，后端 `main.py:69-71` **只注册了** `/api/depth`、`/api/point-cloud`、`/api/repair`，全部 404。
- `components/WorkflowIntegration.tsx:72-82` 用 JSON body POST `/api/depth/estimate`，但 `depth_estimation.py:44-48` 要求 multipart 表单 → 422。
- `WorkflowIntegration.tsx:129` 引用了不存在的变量 `imageFile`（该作用域只有 `imageData: string`）→ **TypeScript 直接编译失败**。
- 没有任何测试跑得通（`test_integration.py` 依赖上面那些缺失模块）。

**断点 C：路径硬编码到另一台机器**
- `services/iopaint_wrapper.py:61`：`--model-dir E:\works\3d-build\models`，这个目录在本机不存在。
- `core/config.py:38-49`：一堆 `../newServer/assets/...` 权重路径。
- `frontend/README.md:84`、`demo.md:66`：安装说明写的还是 `/root/autodl-tmp/demo`（AutoDL 容器路径）。

### 1.4 设计层面的问题（比缺件更值得修）

| 问题 | 位置 | 说明 |
|---|---|---|
| 硬编码相机几何 | `depth_estimation.py:94-95`、`point_cloud.py:190-192`、前端 `PointCloudGenerationPage.tsx:58-59` | 焦距写死 529.5，且交给用户拖滑杆"调"（200–1000）。用错误的焦距反投影，几何就是错的——这是旧管线最大的精度来源问题，却被当成一个"参数"暴露给用户 |
| 深度截断拍脑袋 | `point_cloud.py:387-389` | `0.1 < d < 50.0` 硬阈值，没有置信度概念 |
| `sys.path.insert` 注入依赖 | 两个路由文件顶部 | 模块发现靠魔改 sys.path，导致 import 失败只能 print 不能抛 |
| 模型管理是空壳 | `core/model_manager.py:97-109` | `_load_3d_processing_models()` 只有一行注释和一句"加载完成" |
| 全局单例 + 线程/进程耦合 | `iopaint_wrapper.py:483` + `subprocess` | 后端要管另一个 Python 进程的生死，还带 30 秒轮询 |
| PLY 解析重复实现 | `lib/pointCloudParser.ts`（有单测） vs `PointCloudGenerationPage.tsx:259-355`（内联一份） | 同一件事两份代码，内联那份没有测试 |
| 有单测没跑测 | `pointCloudParser.test.ts` | `package.json` 里没有任何 test runner（无 vitest/jest） |

---

## 2. 值得留下的东西

这是重构的"种子"，别重写：

1. **`components/PointCloudViewer.tsx`（550 行）** —— 这是全项目最有价值的一块。three.js Points + OrbitControls + **PointerLockControls 第一人称漫游（WASDQE）** + 自适应点大小。这部分纯前端、不吃显存、不依赖任何模型，重构后原样保留。
2. **`lib/pointCloudParser.ts`（17.4 KB）+ 它的单测** —— 浏览器端 PLY 解析已经写好了。补个 vitest 就能跑。
3. **FastAPI 骨架** —— `main.py` 的 lifespan / CORS / 静态挂载 / 双异常处理器写法是对的，改内容不改骨架。
4. **`pydantic-settings` 配置模式** —— `core/config.py` 的形态对，只是字段要全换。
5. **设计范式**：`components/ui/*`（Radix + Tailwind + cva）这套 UI 组件可以直接用，删掉不用的几个即可。
6. **产品闭环本身** —— "丢图进去，转出一个能走进去看的 3D 场景"。这个想法没问题，问题在实现。

---

## 3. 要扔掉的东西，和"电脑压力"的账

你现在的压力来自三处，量级完全不同：

### 3.1 磁盘 / 文件数（99.5% 来自 IOPaint）
```
IOPaint-main/            444.7 MB   28096 files   → 整包删除
  iopaint/model/anytext/           SD + OCR 分支
  iopaint/model/brushnet/          SD 分支
  iopaint/model/power_paint/       SD1.5 分支
  iopaint/model/original_sd_configs/  SD 配置
  iopaint/plugins/gfpgan|basicsr|facexlib/  图像复原插件
  iopaint/plugins/segment_anything{,2}/     交互式分割
  web_app/                         另一套前端（未使用）
```
删掉后仓库从 **446.8 MB → ~2 MB**。

### 3.2 显存
旧管线的峰值显存消耗来自：GroundingDINO（~700MB）+ SAM-H（~2.4GB）+ SD/PowerPaint（2–6GB）+ DA2-ViT-L（~1.3GB）。这些模型**不可能同时驻留** 8GB，所以旧代码实际是靠"按需加载/卸载"硬撑，体验上就是每次操作几十秒 + 显存抖动。新管线只有一个模型常驻。

### 3.3 Python 依赖
`backend/requirements.txt` 49 行里，可整体删除的：
`diffusers`、`controlnet-aux`、`transformers`（SD 路径）、`depth-anything-v2`、`segment-anything`、`groundingdino-py`、`albumentations`、`scikit-image`、`python-jose`、`passlib`、`loguru`、`black/isort/flake8`。

`open3d` 单独说：**建议卸载**。它只被用于四件事——统计离群点移除、体素下采样、法线估计、PLY 读写，全部可以用 numpy + scipy 重写（约 150 行，见 §5.1）。open3d 0.19 在 Windows 上是几百 MB 的轮子且依赖链长，对"减负"目标是净亏。

---

## 4. Depth Anything 3 带来了什么

### 4.1 模型货架与 8GB 显存的对应关系

| 模型 | 参数 | 相对深度 | 位姿估计 | 度量深度 | 3DGS | 许可证 | 8GB 上的判断 |
|---|---|---|---|---|---|---|---|
| DA3-SMALL | 0.08B | ✅ | ✅ | — | — | **Apache-2.0** | 最省，可放心用 |
| **DA3-BASE** | 0.12B | ✅ | ✅ | — | — | **Apache-2.0** | **推荐默认档** |
| DA3-LARGE | 0.35B | ✅ | ✅ | — | — | CC BY-NC 4.0 | 质量最好，但**非商用** |
| **DA3METRIC-LARGE** | 0.35B | ✅ | — | ✅ | — | **Apache-2.0** | **家居方向的关键**：输出米制深度 + 天空分割 |
| DA3MONO-LARGE | 0.35B | ✅ | — | — | — | Apache-2.0 | 纯单目，用不上多视图 |
| DA3-GIANT | 1.15B | ✅ | ✅ | — | ✅ | CC BY-NC 4.0 | fp32 权重 ~4.6GB，8GB 上没余量，**别碰** |
| DA3NESTED-GIANT-LARGE | 1.40B | ✅ | ✅ | ✅ | ✅ | CC BY-NC 4.0 | ~5.6GB，**别碰** |

> 峰值显存是估算：BASE 在 `process_res=504`、4 视图下大致 1.5–2.5GB；LARGE 大致 3–5GB。DA3 的 `api.py` 前向自带 `torch.autocast`（bf16/fp16），显存表现比自己手写好。

**许可证是硬约束**：家居是商业场景。要商用/开源发布，只能在 SMALL / BASE / METRIC-LARGE / MONO-LARGE 里选（全 Apache-2.0）。LARGE 和 GIANT 全家 CC BY-NC 4.0 只能自己研究用。这个约束应该写进选型，而不是等做完了才发现。

### 4.2 三个真正的"换代"点

**① 不用再猜相机内参。** DA3 的返回值里直接有：
```
prediction.depth        (N, H, W)      深度
prediction.conf         (N, H, W)      置信度   ← 天然的过滤依据
prediction.intrinsics   (N, 3, 3)      K       ← 取代写死的 529.5
prediction.extrinsics   (N, 3, 4)      E (w2c, opencv 约定)
prediction.processed_images (N,H,W,3)  预处理后的图
```
旧代码里"让用户拖滑杆猜焦距"这个小丑环节可以彻底删掉。

**② 多视图一致。** 同一模型吃 1–N 张图，N 张的深度在同一个世界里一致，位姿自动给出。这是旧管线做不到的（旧管线只能单图 + 手调焦距）。对家居方向，这意味着：**一个房间拍 3–6 张，拼成一个点云**——这才是"3D 重建"而不是"2D 贴片挤出"。

**③ 度量尺度。** `DA3METRIC-LARGE` 输出米制深度（换算：`metric_depth = focal * net_output / 300`）。对家居，没有米制就没有决策价值——"这个沙发能不能放进那个位置"需要的不是好看的深度图，是米。

### 4.3 落地时要绕的坑（已核对上游源码）

| 坑 | 核实结论 | 处理方式 |
|---|---|---|
| `xformers` 是硬依赖吗？ | **不是**。`model/dinov2/layers/swiglu_ffn.py` 里 `try: from xformers.ops import SwiGLU / except ImportError: SwiGLU = SwiGLUFFN`，有纯 PyTorch 回退 | **不装 xformers**。Windows 上按 torch 版本装 xformers 有把 torch 反向改版本的风险，正好避开 |
| `pip install -e .` 会拉多少？ | `pyproject.toml` 的 dependencies 含 `trimesh / open3d / pycolmap / evo / e3nn / moviepy / plyfile / gsplat(extras)`，而且 **`api.py` 顶层就 `from depth_anything_3.utils.export import export`**，而 `utils/export/__init__.py` 又顶层 import `gs / colmap / glb / feat_vis / npz` → 只做深度推理也会被拖入整条导出链 | 改**一行**：把 `_export_results()` 里的 `export(...)` 改成函数内 `from depth_anything_3.utils.export import export`。之后只需要 `torch / torchvision / numpy<2 / opencv / pillow / huggingface_hub / safetensors / einops / omegaconf` |
| Python 版本 | `requires-python = ">=3.9, <=3.13"`，`numpy<2` | 与本机 `3d-build` 环境（py3.10.18 / numpy 1.26.4）匹配 |
| 别用系统 Python | 系统 python 3.13.5 上是 `torch 2.7.1+cpu`，`cuda_available=False` | 必须走 conda 环境 |
| 磁盘 | C 盘只剩 **3.8 GB**；HF 缓存默认在 `C:\Users\<用户名>\.cache\huggingface` | 必须 `HF_HOME=E:\models\da3`（E 盘余 109 GB），否则第一个模型下载就把 C 盘塞满 |
| `process_res` | 默认 504，`upper_bound_resize`（长边缩到 504） | 8GB 上先保持 504；要细节就 672 / 756，显存线性涨 |

---

## 5. 新架构

### 5.1 后端：从"三路由 + 一个外挂进程"缩成"一个引擎 + 一个几何库"

```
backend/
  app.py             ~150 行   FastAPI，4 个端点，lifespan 里预热/释放引擎
  settings.py        ~100 行   pydantic-settings；HF_HOME、模型档位、显存上限、点云预算
  engine.py          ~180 行   DA3Engine：懒加载 / 单例 + 互斥锁 / 切换档位 / 卸载 / 显存快照
  pipeline.py        ~220 行   reconstruct(images, opts) -> ReconstructionResult
  geometry.py        ~260 行   纯 numpy：
                                 · unproject(depth, K) -> 点
                                 · conf 分位数过滤（取代写死的 0.1~50.0）
                                 · voxel downsample（np.floor(p/v).astype(int) 做 key 去重）
                                 · statistical outlier（scipy.cKDTree 的 kNN 距离分位数）
                                 · write_ply(binary_little_endian) —— 比 ascii 小 5~10 倍、解析快
  schemas.py         ~90 行    pydantic 请求/响应模型
```

端点（就 4 个）：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/health` | 设备、模型档位、显存已用/剩余、就绪状态 |
| GET | `/models` | 本地已下载的 DA3 档位 + 各自许可证 + 估算显存 |
| POST | `/reconstruct` | 1–N 张图 + `{model, process_res, max_points, conf_percentile}` → 深度预览图(base64 PNG) + 统计 + 二进制 PLY |
| GET | `/scenes/{id}/pointcloud.ply` | 拉已生成的点云（避免 base64 塞 JSON，大点云会爆内存） |

关键设计：**DA3 藏在 `engine.py` 一个类后面**。下次 DA4 出来，只改这一个文件。这是这次重构最想拿到的东西。

### 5.2 前端：6 页 → 2 页

保留（原样）：`PointCloudViewer.tsx`、`lib/pointCloudParser.ts` + 单测、`components/ui/*`（裁到实际用到的）、`lib/store.ts`、`lib/utils.ts`、`Layout.tsx`

| 页面 | 处理 |
|---|---|
| `/` 重建工作台 | 重写：拖 1–8 张图 → 选模型档 → 进度 → 深度缩略图 + 内嵌 3D 查看器 |
| `/viewer` 查看器 | 保留：打开/拖入本地 PLY |
| `ImageEditorPage` | 删除（konva 交互修复画布） |
| `IOPaintTestPage` | 删除 |
| `SettingsPage` | 删除，缩成侧栏一个"模型/质量"小面板 |
| `HomePage` | 重写 |
| `WorkflowIntegration.tsx` | 重写成 40 行的"上传 → 调用 → 展示"，现在是 383 行且编译不过 |
| `IOPaintEmbed.tsx` / `ImageCanvas.tsx` | 删除 |

`package.json` 从 57 个依赖压到约 14 个：删 `react-konva`、`konva`、`@tanstack/react-query`、`react-hook-form`、`@hookform/resolvers`、`zod`、`immer`、`react-hotkeys-hook`、`react-dropzone`，Radix 只留 `slot/label/slider/progress/toast`，图标库 `@heroicons/react` 与 `lucide-react` 二选一。

### 5.3 依赖账（前后对比）

| | 旧 | 新 |
|---|---|---|
| Python 包 | ~35 | **~12**（torch、torchvision、numpy<2、opencv-python、scipy、pillow、huggingface_hub、safetensors、einops、omegaconf、fastapi、uvicorn、python-multipart） |
| Node 包 | 57 | **~14** |
| 常驻模型 | GroundingDINO + SAM2 + SD/PowerPaint + DA2 | **1 个 DA3** |
| 仓库体积 | 446.8 MB | **~2 MB** |
| 进程数 | 2（后端 + IOPaint） | **1** |

---

## 6. 明确砍掉的功能（你可能心疼的，逐条列出）

**删除：**
1. 图像修复 / 物体移除 / 物体插入 / 物体替换（IOPaint：LaMa、PowerPaint、PowerPaint V2、SDXL、BrushNet、AnyText）
2. 文本驱动掩码（GroundingDINO + SAM / SAM2）
3. 交互式掩码画布（konva 绘图、笔刷、撤销）
4. IOPaint 服务管理：启停、模型热切换、端口/主机配置、输出目录取图
5. 图像修复批量任务接口 `/api/repair/batch`
6. 图像复原插件（GFPGAN / Real-ESRGAN / BasicSR / facexlib）
7. 4 档深度编码器切换（vits/vitb/vitl/vitg）
8. open3d 的增强点云路径（`/generate-enhanced`、法线估计、MLS 平滑、统计+半径双重滤波）
9. `SettingsPage` 里所有服务连接测试、性能参数面板

**保留但升级：**
- 单目深度（DA2） → 单目 + 多视图一致深度 + 自动位姿 + 自动内参
- 硬编码焦距 529.5 → 模型自动估计
- 写死 `max_depth=20` 截断 → 置信度分位数过滤
- 手写反投影 + open3d 后处理 → numpy 干净实现（体素下采样 + 统计离群）
- 点云浏览器查看 + 漫游 → **原样保留**

**新增（这是重构成不成立的唯一理由）：**
- 多图联合重建，N 张图拼成一个统一世界坐标系的点云
- 米制尺度（选 DA3METRIC-LARGE 时）
- 置信度热力图可视化（能直接看出重建哪里不可靠）
- 可选：相机轨迹导出（走 `da3` CLI 子进程出 glb，不把 trimesh 拉进主进程）

---

## 7. 分阶段计划

| 阶段 | 内容 | 验收 | 估时 |
|---|---|---|---|
| **P0 清场** | 删 `IOPaint-main/`、`image_repair.py`、`iopaint_wrapper.py`、`api.ts` 的 IOPaint* 段；删 3 个死页面；`main.py` 能 `uvicorn` 起来且 `/health` 返回 | 仓库 < 3 MB，`/health` 200，`npm run build` 通过 | 0.5 天 |
| **P1 引擎** | 新建 conda env `home3d`(py3.10)；vendor DA3 源码 → `backend/vendor/da3/`；打上 §4.3 的懒加载补丁；`engine.py` 单图跑通 | 一张图返回 depth/conf/K/E，前端显示深度图 | 1–2 天 |
| **P2 几何** | `geometry.py`：反投影 + conf 过滤 + 体素下采样 + 二进制 PLY；前端 viewer 吃新 PLY | 点云在浏览器里能转、能走近、颜色正确 | 1 天 |
| **P3 多视图** | `/reconstruct` 支持 1–8 张；统一世界系融合；加进度/取消 | 3 张同一房间的图 → 一个连续点云 | 1–2 天 |
| **P4 度量与打磨** | 可切 DA3METRIC-LARGE；包围盒 / 房间尺寸估算；`/models` 显示许可证与显存 | 能量出"这面墙 3.2 m" | 1 天 |

总计约 5–7 天有效工作量。P0 + P1 + P2 就有一个能天天用的小工具了。

---

## 8. 环境与磁盘（具体到路径）

**硬件**：RTX 4060 Laptop **8 GB** / Ryzen 9 7845HX / 15.2 GB RAM

**现有可复用**：conda env `3d-build`（`E:\conda\envs_dirs\3d-build`，py3.10.18，**torch 2.5.1+cu121，CUDA 可用**，已有 numpy 1.26.4 / opencv 4.11 / open3d 0.19）

**建议**：**新建**一个 env，不要复用 `3d-build`。理由：`3d-build` 里还留着 `diffusers 0.35.2 / transformers 4.48.3 / pytorch-lightning / torchmetrics` 这套旧的 SD 依赖链，正是这次要甩掉的东西；新 env 从零建，装完能一眼看出到底还剩什么。

```powershell
# 环境（py3.10 对齐 DA3 的 requires-python 与 3d-build 的 torch）
conda create -n home3d python=3.10 -y
conda activate home3d
pip install torch==2.5.1 torchvision==0.20.1 --index-url https://download.pytorch.org/whl/cu121
pip install "numpy<2" opencv-python scipy pillow huggingface_hub safetensors einops omegaconf
pip install fastapi uvicorn python-multipart
# 不装：xformers（有回退）、trimesh、open3d、pycolmap、evo、moviepy、gsplat

# 磁盘：C 盘只剩 3.8GB，模型必须落 E 盘
$env:HF_HOME = "E:\models\da3"
$env:HF_ENDPOINT = "https://hf-mirror.com"   # 国内下载更稳
```

模型体积参考（0.12B ≈ 0.48 GB fp32 / 0.24 GB fp16；0.35B ≈ 1.4 / 0.7 GB）——`E:\models\da3` 放 3–4 个档位完全无压力。

---

## 9. 风险

| 风险 | 说明 | 缓解 |
|---|---|---|
| 许可证 | DA3-LARGE / GIANT 系列 CC BY-NC 4.0（非商用） | 默认档用 DA3-BASE(Apache-2.0)；只要出商业价值就锁死在 Apache 那几个 |
| Windows + CUDA 轮子 | torch 2.5.1+cu121 已在本机验证可用 | 不引入 xformers 就不需要动 torch |
| DA3 上游还在动 | 三个月前还在改 backend dashboard；模型有 `-1.1` 重训版本 | vendor 固定 commit，不跟 `main` 漂 |
| 8GB 显存边界 | 多视图 N 变大时显存线性涨 | `/models` 里按档位给出"最大建议视图数"，超了在 pipeline 里分批 |
| 单图点云的期望管理 | 单图 + 单目深度得到的仍然是**伪 3D**：几何不准，但体积感/交互是真实的 | 产品语义上说清"预览"，把"可测量"留给多视图 + 米制模型 |

---

## 10. 这次重构值得想清楚的 5 个问题

1. **从"修图 + 3D"缩成只做 3D，是退步还是聚焦？** 我的判断是聚焦。旧项目的两半从来不是一件事：修图是 2D 编辑，3D 重建的输入应该是"多张照片"而不是"修好的图"。你砍的是 coupling，不是 feature。
2. **单图点云是伪 3D。** 它的价值不在几何精度，在"能转、能走近、有体积感"。真几何得靠多视图。DA3 恰好把这件事从"研究课题"变成"一次前向"——这是这次换代唯一不可替代的收益。
3. **缺了米制，家居方向就没有决策价值。** "能不能放下这张沙发"要的是米，不是好看的深度图。DA3METRIC-LARGE 是 Apache-2.0，这是白捡的。
4. **8GB 是设计输入，不是缺陷。** 它逼出了"小模型 + 少视图 + 单进程 + 懒加载"这个其实更健康的架构。旧项目的"按需加载/卸载"是为了在 8GB 里塞下 4 个不兼容的大模型,那才是畸形。
5. **模型会过期，接口不会。** 旧项目把 DA2 的调用细节（编码器名、checkpoint 路径、`max_depth`）散布在 config、两个路由、前端设置页里。这次把 DA3 收进 `engine.py`——下次换 DA4，改动应该只有这一个文件。

---

## 附：本次盘点核实的原始依据

- 本地：`code/backend/**`（11 py）、`code/frontend/src/**`（32 文件）、`code/IOPaint-main/**`（28096 文件）
- 上游：`ByteDance-Seed/Depth-Anything-3` 的 `README.md`（模型卡/许可证表）、`docs/API.md`（`inference()` 全参数、导出格式、返回值）、`pyproject.toml`（依赖与 Python 版本）、`src/depth_anything_3/api.py`（顶层 import 链、autocast、位姿对齐）、`src/depth_anything_3/utils/export/__init__.py`（导出链的真实 import 图）、`src/depth_anything_3/model/dinov2/layers/swiglu_ffn.py`（xformers 可选回退）
- 本机：`conda env list`、`3d-build` 的 pip 清单、`nvidia-smi`、磁盘剩余、HF 缓存位置
