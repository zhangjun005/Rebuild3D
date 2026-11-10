<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-01T08:48:28.2596863Z
status: active
<!-- /kron:frontmatter -->

# geometry：数据管线

> 从插画到可渲染点云：深度模型选型、反投影、清洗、重采样。

## 模块边界

本模块只负责"把 2D 变成干净的 3D 点"。怎么画好看不在本模块（那是 `render`）。

## 这个节点（总）

这一层的总决策是**认清上游给的是什么**：DA3 给的是相对深度、没有相机内参，所以只能用正交反投影；点云的"准"不是目标，"能渲染出好看的画面"才是。于是这条管线由四个子决策组成：用哪个模型、怎么投影、选哪些点、散落点怎么清。

## 子意图（细节）

| 子意图 | 它负责的细节 |
|---|---|
| `geometry/depth-model` | 用哪个深度模型（总） |
| `geometry/projection` | 深度怎么变成 3D 坐标（总） |
| `geometry/sampling` | 选哪些点、选多少（总） |
| `geometry/scatter-cleanup` | 层间散落点怎么清（总） |
| `geometry/segmentation` | 角色分割链路（预留，尚未用于渲染） |

> 子意图之间是**并列的细节**，不是执行顺序。执行/推导顺序用 frontmatter 的 `depends_on` 表达。
