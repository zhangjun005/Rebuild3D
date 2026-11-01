# 业务重估 · 点云编辑现状 · Go/Python 分工

> 前三节基于 2026-11 对 GitHub 上游的直接核对（star/commit/许可证以仓库为准）
> 第四节是架构决策，第五节是 kron 的落地用法

---

## 1. 业务重估：旧定位为什么必然冷却

### 1.1 旧项目实际是两个产品粘在一起

```
产品 A：AI 修图（IOPaint / GroundingDINO+SAM2 / SD 全家桶）
产品 B：单图深度 → 点云（DA2 + 手写反投影）
粘合剂：把修好的图喂给深度模型
```

这条粘合逻辑本身就站不住：**修图的产物是"好看的 2D 图"，而 3D 重建需要的是"多视角 + 位姿"的信息量**。修图不增加任何几何信息，它只是让输入图更干净。所以两者的组合没有产生 1+1>2，只是把两个各自都很难商业化的东西捆成一个更难维护的东西。

### 1.2 家居方向冷却的真实原因：不是技术不行，是打不过 LiDAR 且没人付费

| 想做的事 | 技术路径 | 现实 |
|---|---|---|
| 拍照量房 | 单目深度 → 平面/尺寸 | iPhone Pro 有 LiDAR，精度直接碾压单目。Canvas、Polycam 这类产品已经占住"拍照量房"的坑 |
| 房间 3D 预览 | 单目深度 → 点云 | 用户觉得"挺酷"，但不付费。点云本身不能交付给下游（设计师要 CAD、电商要 glTF） |
| 家具试摆 | 需要房间模型 + 家具资产 + 编辑 | 这是真需求、真付费场景，但环节最长、资产最重 |

**结论：单图深度做出来的点云，上限就是一个 demo。** 它不精确（无法测量）、不完整（背面没有）、不能交付（不是网格/材质/单位）。这就是热度退潮的根本原因——不是模型不够好，是交付物不对。

### 1.3 真正变化了的三件事

1. **多视图前馈重建成熟了**（DA3 一次前向出 depth + conf + K + E）。"拍 3–6 张照片拼一个房间"从研究课题变成调 API。这改变了"信息量不足"这个根本约束。
2. **浏览器端 3D 编辑成熟了**（见第 2 节）。编辑不再是桌面软件的特权。
3. **image→3D 资产生成成熟了**，但**不在你的硬件预算内**——见 2.4。

### 1.4 三个可选定位，价值梯度差很多

| 定位 | 交付物 | 技术闭环 | 商业价值 | 8GB 可行性 |
|---|---|---|---|---|
| **A. 空间预览工具**（自用/内容创作） | 一个能走进去看的点云/3DGS 场景 | 1–2 周 | ≈0 | ✅ |
| **B. 家居电商试摆** | 房间模型 + 家具资产 + 摆放 + 出图 | 3–6 月 | **高** | ❌ 资产生成跑不动 |
| **C. 纯前端 3DGS 编辑器** | 一个编辑器 | 2–3 月 | 低（等于重做 SuperSplat） | ✅ |

### 1.5 我的建议：别当产品做，当"能力基座"做

你自己说的是"想获得一些思考"。那目标函数就不是"做成生意"，而是**用最小可运行闭环，验证三个技术判断**：

1. 多视图前馈重建在 8GB 上能不能做到"一次操作 = 十几秒"？
2. 浏览器端编辑能不能做到"不装软件、点一下就能删"？
3. Go 做产品层 + Python 做推理壳，这个缝是不是干净的？

**这三个问题的答案比一个不上不下的产品有价值。** 而且它是可持续的——每次模型换代，你只换 sidecar。

**如果哪天真要做生意，撬点是"编辑"，不是"重建"。** 重建是现成模型的能力，谁都能调；编辑是最后一公里，现在没有好用的东西——见下一节。

---

## 2. 点云编辑的外面世界（2026-11 核对）

**一句话结论：这个领域的重心已经从"生成点云"移到"编辑点云"，而且赢家全在浏览器里。**

### 2.1 SuperSplat（playcanvas/supersplat）—— 必须先看的东西

- **MIT 许可证**（可直接参考甚至摘代码）
- 787 commits、264 tags，最后一次提交 2026-10-29（几天前），仍在活跃
- 官网标注 **Editor 3.0 重建在 WebGPU 上**
- 功能面：选择工具、变换（移动/缩放）、点着色、径向排序、撤销、优化、发布/分享
- **关键细节**：最近一次重构的提交信息是
  > *"Add a typed editor API over serialized selection commands"*
  紧接着 *"Reject invalid editor commands and keep colour points apart"*

  这说明它把编辑实现成**可序列化的命令流 + 类型化 API**，而不是直接改内存里的点。这个设计选择直接决定了后面第 4 节 Go 该怎么切。**这是整个调研里最有价值的一条。**

### 2.2 Spark（sparkjsdev/spark）—— 和你的技术栈直接可用

- World Labs 出品，**three.js 的 3DGS 渲染器**
- 支持 `ply / sogs / spz / splat / ksplat`
- 能和 mesh 物体在同一 three.js 场景里融合渲染
- 用法就是 `npm install @sparkjsdev/spark` + `new SparkRenderer({renderer})`

**意义**：你现有的前端就是 three.js。想从"点云"升级到"3DGS 可漫游场景"，不需要换渲染栈，加一个库 + 换成 `SplatMesh` 即可。DA3 的 GS head（需 GIANT 系列，CC BY-NC）可以产 3DGS——但 8GB 装不下，见 1.4。

### 2.3 语义选择：把"删掉那个东西"变成可能

| 项目 | 做的事 | 备注 |
|---|---|---|
| **SAGA**（Jumpat/SegAnyGAussians, AAAI'25） | 2D mask → 3D 高斯分割 | 有 GUI，点 `segment3D` 出结果 |
| **FlashSplat**（florinshen/FlashSplat, ECCV'24） | 2D mask → 3D 高斯分割 | 宣称 **~30 秒、约 50× 加速** |

这是"用户点一下就把沙发删掉"的最后一层拼图。**注意它们都是给 3DGS 用的，不是给裸点云用的**——如果你只做点云，这条路要自己补（点云上的选择可以用深度/法线/连通性做，简单很多）。

### 2.4 生成侧（家具资产）—— 你的硬件够不着，先别碰

| 模型 | 规模 | 硬件要求 | 许可证 |
|---|---|---|---|
| TRELLIS (microsoft/TRELLIS) | TRELLIS-image-large **1.2B** | 官方：**"至少 16GB 显存"**，仅 Linux 验证 | 见仓库 |
| Hunyuan3D 2.1 | 3.3B shape + 2B paint | 第三方总结：全流程 **≈29GB** | 社区许可，**排除 EU/UK/韩国** |

**结论：image→3D 家具资产在 8GB Laptop 上不是"慢一点"，是"要先做大量显存优化工程"。** 这一块应该明确划到范围外，否则整个项目会被它拖死。网上确实有"优化到 8GB"的民间说法，但那是额外工程，不是开箱能力。

### 2.5 抽象出来的编辑能力清单（这才是产品差异点）

按实现难度排序，前 5 个是"一个人两周内能做完"的：

1. **框选 / 球选 → 删除点**（最简单，也最常用：删掉画面里的人、杂物）
2. **变换（移动/旋转/缩放）选中块**（挪动家具）
3. **点着色 / 颜色修正**（改地板颜色）
4. **裁剪盒 / 只保留房间内部**（去掉墙外噪点）
5. **测量：两点距离、包围盒、地面高度**（家居的核心价值）
6. 语义选择（点一下就选中物体）—— 需要 SAM 系模型，可选
7. 撤销/重做（1–5 做全了自然就有了）

**关键洞察**：这 5 项全部可以在**浏览器里**做完，后端只负责"存命令 + 出初始点云 + 导出"。这个分工是第 3、4 节的前提。

---

## 3. Go 能承担什么，不能承担什么

### 3.1 不能承担的（别浪费时间）

| 想做的事 | 为什么不行 |
|---|---|
| 在 Go 里跑 DA3 | Go 的 ML 生态≈0。`onnxruntime-go` 要 CGO，且 DINOv2 变体 + 自定义 ray/camera head 导出 ONNX 极其痛苦，动态 shape 还不一定支持 |
| 点云重算法 | KD-tree / 法线估计 / Poisson 重建 / 网格化，Go 的库远不如 numpy/scipy/open3d。自己写等于重造轮子 |
| 图像/几何科学计算 | 同理。Go 强在并发和 IO，不强在 ndarray |

### 3.2 应该承担的（这是 Go 的主场）

| 职责 | Go 生态 | 说明 |
|---|---|---|
| HTTP / SSE / WebSocket | `go-chi/chi`、`gin` | 大点云分块推送、进度流 |
| 任务编排 | in-process worker + `modernc.org/sqlite`（**纯 Go，无 CGO**）；要队列上 `riverqueue/river`(PG) 或 `hibiken/asynq`(Redis) | 单机起步零依赖 |
| 存储 / 上传下载 / 文件 | 标准库 + `os`/`io`，要对象存储上 MinIO SDK | 静态单二进制部署 |
| **编辑命令流** | `encoding/json` 或 `vmihailenco/msgpack` | 见 3.3，这是关键 |
| 几何后处理 | 自写 + `gonum` | 体素下采样、AABB、统计离群、PLY 二进制读写（自写约 150 行；Go 没有靠谱的 PLY 库，但这格式简单） |
| 空间索引 | `tidwall/rtree` 或自写 grid hash | 框选/球选的点查询 |
| glTF / GLB 导出 | `qmuntal/gltf` | three.js 直接吃 |
| CLI / 批量 | `cobra` 或标准库 `flag` | 批量重建、导出 |
| 鉴权/多用户 | 标准库 + `golang-jwt` | 真有需要时再加 |

### 3.3 关键洞察：编辑应该是"命令日志"，不是"改数据"

SuperSplat 的最新提交（*typed editor API over serialized selection commands*）证明了这个方向。把编辑建模成：

```go
type EditCommand struct {
    Op      string  // "delete" | "transform" | "paint" | "crop" | "measure"
    Select  Selector // box | sphere | indices | semantic
    Params  map[string]any
    Seq     int
    Author  string
    Time    time.Time
}
```

带来的额外能力，几乎零成本：

- **撤销/重做** = 截断/重放日志
- **持久化** = 存日志（几百字节），不存改过的几十 MB 点云
- **协作** = 日志是 append-only 的，天然可合并
- **审计** = 谁在什么时候删了什么
- **非破坏性** = 原始点云永不改变，随时可回到初始

**这件事在后面做后端的时候，用 Go 写会非常顺手；用 Python 写会非常别扭。** 这是让 Go 承担"编辑后端"最正当的理由。

### 3.4 划界：Python 缩成一个 300 行的 sidecar

```
┌─────────────────────────────────────────────┐
│  Go 单二进制（产品）                          │
│  chi router · SSE 进度 · 任务队列(SQLite)       │
│  编辑命令流 · 空间索引 · PLY/GLB 读写           │
│  geometry: unproject / voxel / outlier        │
└───────────────┬─────────────────────────────┘
                │ gRPC 或 HTTP+msgpack（二进制）
                │ Reconstruct(images, model, res)
                │   -> depth[], conf[], K[], E[]
┌───────────────▼─────────────────────────────┐
│  Python sidecar（≈300 行，进程常驻）           │
│  torch + DA3 + numpy + cv2 只做推理            │
│  不碰几何、不碰存储、不碰 HTTP 业务             │
└─────────────────────────────────────────────┘
```

**为什么这样切比"全 Python"好：**

1. 你 90% 的代码写在自己的主语言里（这是你说"主要做 Go"的直接诉求）
2. 换 DA3 → DA4，只换 sidecar，proto 不变
3. Python 环境变成一个可随时丢弃的容器/子进程，它的依赖地狱不再污染产品
4. **显存边界清晰**：只有 sidecar 摸 GPU，Go 侧永远不会因为 import torch 崩掉
5. 部署 = 一个 Go 二进制 + 一个 Python 进程（开发机上就是 `go run` + `uvicorn`）

**代价**：Go 侧要实现点云几何（约 300–400 行），不如 numpy 一行 `points = d[...,None]*K_inv@...` 短。这是明确的取舍，接受它。

---

## 4. kron 的用法（配合这个项目）

`.kron/` 已在 `E:\works\Rebuild3D` 初始化（`config.toml` + `anchor-scan.json` + `templates/` 齐备，`intents/` 为空）。建议先落三条 intent，把"为什么这么定"固化：

| slug | 内容 |
|---|---|
| `product/positioning` | 定位 A（能力基座）而非产品；砍掉修图线；交付物是"可浏览 + 可编辑的场景" |
| `architecture/go-python-split` | Go 做产品层与几何，Python sidecar 只做推理；proto 边界 |
| `editing/command-model` | 编辑 = 可序列化命令日志，非破坏性；撤销=重放 |

外加两条 assumption（因为它们是**会被时间证伪**的技术断言）：

| id | 断言 | 校验方式 |
|---|---|---|
| `da3-base-fits-8gb` | DA3-BASE @504px、≤6 视图在 8GB 上峰值 < 6GB | 实测 `torch.cuda.max_memory_allocated()` |
| `xformers-optional` | 不装 xformers 时 DA3 可正常推理 | 已从源码确认有 ImportError 回退，需跑一次验证 |

---

## 5. 参考链接

- SuperSplat 编辑器：[playcanvas/supersplat](https://github.com/playcanvas/supersplat) · [在线版](https://superspl.at/)
- three.js 的 3DGS 渲染器：[sparkjsdev/spark](https://github.com/sparkjsdev/spark) · [文档](https://sparkjs.dev/)
- 3DGS 语义分割：[SAGA](https://github.com/Jumpat/SegAnyGAussians) · [FlashSplat](https://github.com/florinshen/FlashSplat)
- image→3D 生成（超出 8GB 范围）：[microsoft/TRELLIS](https://github.com/microsoft/TRELLIS) · [Hunyuan3D-2.1](https://github.com/tencent-hunyuan/hunyuan3d-2.1)
- 深度/重建基座：[ByteDance-Seed/Depth-Anything-3](https://github.com/ByteDance-Seed/Depth-Anything-3)
- kron：[zhangjun005/Kron](https://github.com/zhangjun005/Kron)（MIT，v0.3.0，18 个 `kron_*` 工具）
