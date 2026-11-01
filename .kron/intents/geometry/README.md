<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-01T08:48:28.2596863Z
status: active
<!-- /kron:frontmatter -->
# geometry：数据管线

> 从插画到可渲染点云：深度模型选型、反投影、清洗、重采样。

## 模块边界

本模块只负责"把 2D 变成干净的 3D 点"。怎么画好看不在本模块（那是 `render`）。

## 子意图

- `geometry/depth-model-choice` —— DA3 模型选型与 vendor 策略

## 待补（后续空位）

- 反投影：透视 / 正交两种投影
- 深度边缘剔除（消 flying pixels）
- 重采样：voxel / 蓝噪，点数由目标密度决定
- 背景延展（补洞 L1，零模型）
