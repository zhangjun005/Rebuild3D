<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-01T08:48:28.3335882Z
status: active
<!-- /kron:frontmatter -->

# render：风格化渲染

> > 这是整条线的价值核心。

数据管线是苦活，风格才是卖点。

## 模块边界

本模块只定"风格如何组织与组合"。点在 3D 里怎么来不在本模块（那是 `geometry`）。

## 完整清单

7 种风格、5 种基元、参数总表、6 套预设、实现坑：`docs/rendering-styles.md`

## 这个节点（总）

这一层的总决策：**风格不是滤镜，是分层的**（基元 / 风格 / 时间）。同时把"观察方式"（相机 FOV、画布底色）也归到这里，因为它们与模型输出无关、纯粹是"怎么看"。方法论的出处与取舍单独记录。

## 子意图（细节）

| 子意图 | 它负责的细节 |
|---|---|
| `render/style-layering` | 风格化三层结构（总） |
| `render/camera-fov` | FOV 与模型无关，越小越接近正交 |
| `render/canvas-vs-paper` | 画布底色与纸色是两个概念 |
| `render/methods-inventory` | 55 项渲染范式清单与进度表 |
| `render/method-provenance` | 论文出处与哪些该采纳 |

> 子意图之间是**并列的细节**，不是执行顺序。执行/推导顺序用 frontmatter 的 `depends_on` 表达。
