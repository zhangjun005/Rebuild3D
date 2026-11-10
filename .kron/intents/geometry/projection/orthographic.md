<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:35.0770008Z
<!-- /kron:frontmatter -->
# 正交反投影：没有内参就不假装有相机

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

因为模型没有内参、深度是相对的，透视反投影是"假装有一个不存在的相机"。正交投影 x=(u-cx)·s、z=-d 里 s 是唯一的自由参数，正好匹配这个事实。实现为可混合形式（perspective=0 纯正交 / 1 纯透视），orthoScale 取 dRef/focal 使两者在代表深度处重合，切换时尺寸连续。验证：正交下 x 跨度 0.702 与深度无关，z 跨度可独立压缩。
