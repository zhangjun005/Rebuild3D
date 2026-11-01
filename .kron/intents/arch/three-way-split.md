<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-01T09:55:09.7812171Z
status: active
assumptions:
    - id: da3-mono-fits-8gb
      severity: hard
      rationale: 一旦破裂就只能降分辨率或降档，直接影响画质目标与端到端耗时
    - id: xformers-optional
      severity: hard
      rationale: 破裂后要在 Windows 上冒险改动底层张量库版本，可能连带毁掉整个 CUDA 环境
      verified_at: "2026-11-01"
      verified_by: '@zhangjun005'
    - id: da3-export-import-chain
      severity: hard
      rationale: 若判断有误，按上游默认方式安装会把一整串图形与导出依赖塞进环境，甚至直接导入失败
      verified_at: "2026-11-01"
      verified_by: '@zhangjun005'
depends_on:
    - product/illustration-pointcloud
<!-- /kron:frontmatter -->
# 三段式职责切分：Python / Go / 浏览器

> Python 只做推理，Go 做几何与服务，渲染只在浏览器。两条边界分别是 stdio 管道和 HTTP。

## 为什么（Why）

先问"渲染放哪"。答案是只能放浏览器：风格化的价值全在 shader，而服务端渲染要么用 headless
浏览器（重、慢、多一层不确定性），要么自己写软件光栅化器（对点云 splat 来说工作量太大）。

定了这一条，剩下两块的边界就自然出来了：

- **Python 只做推理**，缩成约 300 行的常驻 sidecar，不碰几何、不碰存储、不碰网络业务。
  好处是换模型（DA3 → DA4）只换 sidecar，协议不变。
- **Go 做所有脏活**：进程管理、任务编排、二进制协议、点云几何、导出。
  这是用户的主语言，而进程管理 + 并发 + 单二进制分发正好是 Go 的舒适区。

**边界不交叉**：视觉状态（相机、风格参数、动画时间）只在浏览器；模型与显存只在 Python；
进程、文件、中间缓冲只在 Go。

## 如何（How）

两条边界，两种传输，都不走 JSON/base64：

| 边界 | 传输 | 格式 |
|---|---|---|
| Go ↔ Python | **stdio 帧协议**（子进程管道，无端口、无网络） | 4 字节长度前缀 + 定长头 + 原始图片字节 / `depth`+`conf` float32 |
| Go ↔ 浏览器 | HTTP | `"ILC1"` + count + pos f32[3n] + col u8[3n] |

完整帧定义见 `docs/selection.md` §4。Go 侧职责清单见 `docs/go-scope.md`。

Go 侧**尽量零第三方依赖**：标准库 `net/http` + Go 1.22+ 的 `ServeMux`、手写反投影、
自写二进制 PLY。点云算法优先评估现成的 `seqsense/pcgol`（有 VoxelGrid、空间索引、SAC），
不重用再自己写。

## 权衡（Tradeoffs）

- Go 要自己写点云几何，约 300~400 行；numpy 一行 `points = depth * K_inv @ ...` 就能做的事，
  Go 要写循环。**但能按行并行、内存可复用，且没有巨大依赖树**——这个交换是愿意付的。
- 多一个 sidecar 进程要管（启动、健康检查、崩溃重启、显存释放）。这部分复杂度转移到 Go，
  也是它最容易被低估的一块工作。
- stdio 比 HTTP 难手工调试（要写个小测试工具，或临时加 `--http` 开关）。

## 备选（Alternatives）

- **全 Python**：生态最好，但部署重（依赖树大）、且不是主语言
- **全 Go**：跑不了模型。`onnxruntime-go` 要 CGO，且 DINOv2 变体 + 自定义 head 导出 ONNX 极痛苦
- **Go → WASM 把几何跑在浏览器**（`seqsense/pcdeditor` 的路子）：v1 不需要，但为"自包含 HTML 可编辑"留了门
