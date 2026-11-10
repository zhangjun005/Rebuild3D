<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:59:18.3475735Z
<!-- /kron:frontmatter -->
# composite

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

点云渲染的最后一段是合成 pass：把点云颜色图 + 平滑深度图合成最终画面。这一层决定了"印刷质感"能不能成立——半调网点、铜版排线、描边都在这里做，而不是在点里做。放在渲染域下单独成组，因为它们的实现约束（必须全屏、必须能独立开关、关掉要像素级等于未加该 pass）跟点基元完全不同。
