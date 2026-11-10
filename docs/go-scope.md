# Go 在这一版里的职责边界

<!-- @kron:intent arch/three-way-split -->

> 回答一个问题：**插画 → 点云 → 风格渲染这条线上，Go 能做什么？**
> 结论先说：**Go 是骨架和肌肉，shader 是脸。** 骨架不显眼，但决定东西会不会崩。

---

## 0. 先诚实定位

这条线的**价值**集中在风格化渲染，而风格化渲染**必须在浏览器**（WebGL/WebGPU shader）。这意味着：

- **不是** "Go 写后端，前端只是壳"
- **是** "Go 干所有脏活，前端干所有有创意的事"

按代码量估：

| 部分 | 语言 | 约行数 | 占比 |
|---|---|---|---|
| 服务 + 编排 + 几何 + 导出 | **Go** | ~1500 | ~45% |
| 推理 sidecar | Python | ~300 | ~9% |
| 渲染 + 交互 + shader | TS/GLSL | ~1500 | ~45% |

所以 Go 不是配角，是**占比最大的两块之一**。只是"最出彩的那部分"不归它。

---

## 1. Go 做八件事

### ① HTTP 服务 + 静态资源打包（单二进制）

- **标准库 `net/http` + `ServeMux`**（Go 1.22+ 已支持 method + path pattern，不需要 chi）
- `go:embed` 把 `web/dist` 打进二进制
- 结果：**一个 exe，双击就跑，没有 node_modules、没有 Python 环境依赖**（Python 是独立 sidecar）

### ② sidecar 生命周期管理（保姆职责，最容易出事的活）

| 职责 | 说明 |
|---|---|
| 启动 | 拉起 Python 进程，注入 conda env 路径 |
| 预热 | 首次 `/warmup` 触模型加载（30~120s），期间前端显示"正在加载模型" |
| 健康检查 | 定时 `/health`，拿显存占用 |
| 崩溃重启 | sidecar 挂了自动重启一次，并向 UI 报事件 |
| 优雅关闭 | 退出时先 `/unload` 释放显存，再杀进程 |
| 日志透传 | sidecar 的 stdout/stderr 转发到 Go 日志（带前缀） |

**为什么放 Go**：这是进程管理 + 超时 + 重试 + 并发安全，Go 的舒适区；Python 里写会很别扭。

### ③ 任务编排与进度推送（SSE）

单机、单用户、并发少 → **不引任务队列**（不要 Redis、不要 SQLite）。

```
上传 → [深 度] → [几何] → [序列化] → 返回
        ↓ SSE 推送阶段与耗时
```

一个内存里的状态机 + SSE 足够了。需要的话再上 `modernc.org/sqlite`（纯 Go 无 CGO）。

### ④ 点云几何（★ Go 真正吃肉的算法部分）

| 功能 | 说明 | 并行 |
|---|---|---|
| **反投影** | depth + 焦距 → 3D 点；**透视 / 正交两种**（正交是一个开关，见 [scope.md](scope.md)） | `goroutine` 按行切分 |
| **深度边缘剔除** | 相邻像素深度跳变 > 阈值 → 判遮挡边界，两侧剔除（消 flying pixels） | ✅ |
| **置信度过滤** | 按 `conf` 分位数剔除（若模型无 conf 则退化为深度梯度） | ✅ |
| **重采样** | voxel 化每格留代表点 / 蓝噪采样。**点数由目标密度决定，与图像像素无关** | ✅ 分桶并发 |
| **法线估计** | 用于描边、光照、笔触朝向 | ✅ |
| **包围盒 / 归一化** | 场景居中、缩放到统一尺寸，前端不用猜 | — |

**这是 Go 在这条线上最有价值的一块**：纯数值、 embarrassingly parallel、无框架依赖，而且写起来比 numpy 版更可控（numpy 一行 `points = d * K_inv @ ...` 确实更短，但 Go 版能按行并行、内存可复用）。

### ⑤ 二进制协议编解码

两个格式都由 Go 读写（规范见 [selection.md](selection.md) §4）：

```
sidecar → Go :  "DPT1" + W/H/flags + depth f32[] + conf f32[]
Go → 浏览器   :  "ILC1" + count + pos f32[3n] + col u8[3n]
```

用 `encoding/binary` + `unsafe.Slice`，零 JSON、零 base64。**这是性能上最容易拿到收益的地方。**

### ⑥ 导出

| 格式 | 归属 | 说明 |
|---|---|---|
| **PLY**（binary_little_endian） | **Go** | 自写 ~150 行，给 Blender/MeshLab |
| 项目文件（`.json` + `.ply`） | **Go** | 保存参数与风格预设，可复现 |
| **WebM** | **浏览器** | `MediaRecorder` 内置，零依赖 |
| GIF | 浏览器（后续） | 需要 JS GIF encoder；Go 的 `image/gif` 需要帧，而帧在浏览器产生 |
| **自包含 HTML**（见 §4） | **Go** | 把点云 + viewer + 预设打成单个 HTML |

### ⑦ 配置与风格预设管理

[rendering-styles.md §7](rendering-styles.md) 的六套预设存成 JSON，Go 读出来通过 API 给前端。

> 好处：**加一套风格不用改代码**，改一行配置即可。

### ⑧ CLI 模式（可选，但很适合你）

```powershell
illust3d batch .\input\*.png --out .\out --preset stipple-print
```

批量出点云 + 预览图（不做渲染）。对"处理一个文件夹的插画"很实用，也是 Go 单二进制的天然优势。

---

## 2. Go 不做什么（以及为什么）

| 不做 | 原因 |
|---|---|
| **跑模型** | Go 的 ML 生态≈0。`onnxruntime-go` 要 CGO，且 DINOv2 变体 + 自定义 head 导出 ONNX 极其痛苦 |
| **渲染（shader）** | 必须在浏览器。服务端渲染要 headless Chrome（重）或自己写软件光栅化器（太重） |
| **GIF 编码** | 帧在浏览器产生，绕一圈回 Go 没有收益 |
| **图像预处理（重）** | resize/色彩空间这类简单操作用 Go 标准库即可；复杂的（inpainting 补洞）在 Python |
| **真正的三维重建/网格化** | 这条线不需要。若以后要（点云→mesh），那是另一个课题，届时用 Python + open3d |

---

## 3. 技术选型：尽量零第三方依赖

**目标：Go 侧只依赖标准库 + 最多 1~2 个包。** 契合"不要乱塞东西"。

| 用途 | 选择 | 理由 |
|---|---|---|
| 路由 | **标准库 `net/http` + `ServeMux`** | Go 1.22+ 已支持 method + pattern，chi 非必需 |
| 并发编排 | **标准库** `sync` / `context` | 够用；需要时加 `golang.org/x/sync/errgroup` |
| 数值 | **手写** | 反投影是几行数学，不需要 gonum |
| KNN（法线/离群） | 自写 **grid hash**；不够再用 `tidwall/rtree` | 点云空间查询用均匀网格比 KD-tree 更简单更快 |
| PLY | **自写** ~150 行 | 二进制 PLY 格式很简单；Go 生态没有靠谱维护的库 |
| 配置 | 标准库 `encoding/json` | 不引 TOML 库 |
| 静态资源 | `go:embed` | 标准库 |
| 日志 | 标准库 `log/slog` | Go 1.21+ |

环境事实：Go **1.27.1**，`GOPROXY=https://goproxy.cn`，`GOMODCACHE=D:\gopath\pkg\mod`（不在 C 盘）✅

### 3.1 但先别急着自己写：Go 生态里已有两个直接相关的项目（2026-11 核对）

**这条是重要的现实纠正**——Go 的点云生态不是空白，只是很窄。

| 项目 | 说明 | 许可证 | 活跃度 |
|---|---|---|---|
| **[seqsense/pcgol](https://github.com/seqsense/pcgol)** | **Go 的点云处理库，明确"不是 PCL 的移植"**。已有包：`mat`（线性代数）、`pc`（二进制点云序列化 + 迭代器）、**`pc/filter`（VoxelGrid 下采样）**、**`pc/storage`（空间索引）**、`pc/segmentation`、**`pc/sac`（RANSAC 类模型拟合）** | Apache-2.0 | 2026-09 仍在提交，85 commits |
| **[seqsense/pcdeditor](https://github.com/seqsense/pcdeditor)** | **浏览器端点云编辑器**：Go 编译成 **WebAssembly** + React 前端。有 `command.go` / `console.go` / `history.go` / `undo.go`，还有 **`ortho` / `perspective` 投影切换**、`voxel_grid` 命令、`point_size`、标签与分割 | Apache-2.0 | 2026-10 仍在提交，497 commits |

**对我们的直接影响：**

1. **`pc/filter` 的 VoxelGrid 和 `pc/storage` 的空间索引，正是我计划手写的东西。** 先花半小时评估 pcgol 能不能直接用；能用就省 200~300 行，不能用再自己写。
   ⚠️ 但只有 15 stars，本质是日本机器人公司 SEQSENSE 的内部库开源。**要评估代码质量，不能盲信。**
2. **pcdeditor 验证了三个我们已定的设计**：编辑用命令流（`command.go` + `history.go` + `undo.go`）、投影可在正交/透视间切换、voxel 下采样是一等参数。
3. **pcdeditor 还开了一个我没想到的选项**：**Go → WASM，让点云几何直接跑在浏览器里**。v1 不需要（几何在服务端跑 <100 ms），但如果以后想让"自包含 HTML"真的能改点云，这是现成的路径。
4. **PLY 仍然要自写**：pcgol 是 PCD 体系（`pcd_js.go`、`map.pcd`），没有 PLY。搜索 `ply mesh pointcloud language:go` 在 GitHub 上**返回 0 个仓库**，印证了这一点。

---

## 4. 一个值得做的小东西：自包含 HTML 导出

把「点云二进制 + viewer + 风格预设」用 `go:embed` 打成一个**单文件 HTML**，双击就能在浏览器里打开，不需要起服务。

**为什么值得**：
- 这是"分享"的最小单位。动图要编码，HTML 不用
- 完全发挥 `go:embed` 的优势
- 拿到文件的人不需要装任何东西

**代价**：点云二进制作 base64 内嵌 → 体积 ×1.33。10 万点约 1.2 MB → HTML 约 1.6 MB。可接受。

---

## 5. 一句话总结

> **Python 负责"看懂图"，浏览器负责"画得好看"，Go 负责"把这两件事可靠地串起来，并算好中间那堆点"。**

如果只记一句：**Go 的护城河是几何 + 进程管理 + 零依赖分发，不是渲染。**
