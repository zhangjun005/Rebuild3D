<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:34.9966677Z
<!-- /kron:frontmatter -->
# relative depth no intrinsics

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

实测 DA3MONO-LARGE 的 Prediction：depth 范围 [0.059,1.271] 且**无量纲**；is_metric 是空 dict、scale_factor=None、intrinsics=None、extrinsics=None、conf=None，只附带一个 sky 掩膜（1.8%，该处深度=1.264 即最远层）。结论：它是**相对深度 + 无相机内参**。这条事实决定了反投影必须用正交——透视公式 x=(u-cx)·d/f 里 f 是猜的、d 无量纲，等于两个假设相乘，"远层被放大"是这个假设的产物而非模型信息。
