<!-- kron:frontmatter -->
symbol:
    - illust3d
created_by: '@zhangjun005'
updated_at: 2026-11-01T08:47:59.4375938Z
status: active
assumptions:
    - id: depth-on-illustration-usable
      severity: hard
      rationale: 如果插画的深度输出不可用，几何点云方案要整体退回 2.5D 分层视差，技术方案与阶段计划全改
<!-- /kron:frontmatter -->

# 主线：插画 → 点云 → 风格化渲染

> > 一张插画进，一个能转能动、有画风的东西出。

目标是"好看"，不是"准确"。

## 为什么（Why）

旧主线是"空间采集 / 户型"（见 `docs/archive/01`、`02`）。它的 go/no-go 关卡排在流程第 4 步，
要先搭 3 天才能知道"俯视投影能不能看出户型"。

插画线把第一份反馈压缩到**第一天**：一张插画跑深度、看一眼，就知道行不行。

更关键的差别是目标函数。空间线卖的是"可测量、可交付"，于是每一环都要过精度论证；
插画线卖的是画面，**所有精度焦虑在这条线上不存在**——插画本来就不真实，深度的失真反而是可控的创作材料。

## 权衡（Tradeoffs）

换来的自由：
- 不需要米制尺度，不需要和卷尺比精度
- 不需要第二个大模型（不做 image→3D 资产生成，TRELLIS/Hunyuan3D 在 8GB 上跑不动）
- 深度的 OOD 问题从"缺陷"变成"风格"

付出的代价：
- **没有下游，也没有商业目标**。产出是好看的动图/网页，不是可用的模型
- 之前积累的多视图、米制、户型判断力，这条线上用不到
- 最大的单点风险：插画上的深度可能退化成纸片（见假设 `depth-on-illustration-usable`）

## 备选（Alternatives）

- **A 方向（户型/尺寸）**：没有废弃，留在 `docs/archive`。深度引擎共用，随时可切回，go/no-go 只要 1 天
- **单品转台（360°）**：技术上同一条链路，但观感不如 3DGS、卖相不够，放到 v2

---

## 子意图（细节）

| 子意图 | 它负责的细节 |
|---|---|
| `product/illustration-pointcloud/stylization-modes` | 风格化输出分三层，不是一层 |
| `product/illustration-pointcloud/output-stills-only` | 只输出静态图片 |
| `product/illustration-pointcloud/scope-cuts` | 主动砍掉了什么 |
| `product/illustration-pointcloud/video-output` | 已废弃：GIF/视频导出（superseded） |

> 子意图之间是**并列的细节**，不是执行顺序。执行/推导顺序用 frontmatter 的 `depends_on` 表达。
