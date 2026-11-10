<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:34.8994475Z
<!-- /kron:frontmatter -->
# data

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

跨三段的只有两份数据：ILC1 点云字节流和 sidecar 的 stdio 帧。这两份格式的每个字节都被性能与兼容性约束过（零拷贝、向后兼容、三档 mask 的踩坑），是三条链路里最容易一改就炸的地方。
