<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-01T08:48:28.3335882Z
status: active
<!-- /kron:frontmatter -->
# render：风格化渲染

> 这是整条线的价值核心。数据管线是苦活，风格才是卖点。

## 模块边界

本模块只定"风格如何组织与组合"。点在 3D 里怎么来不在本模块（那是 `geometry`）。

## 子意图

- `render/style-layering` —— 风格化拆成三层（基元 / 风格 / 时间）
- `render/density-as-style` —— 点密度是风格参数，不是质量指标

## 完整清单

7 种风格、5 种基元、参数总表、6 套预设、实现坑：`docs/rendering-styles.md`
