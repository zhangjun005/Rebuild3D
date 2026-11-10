<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:57.2774302Z
<!-- /kron:frontmatter -->
# evidence first

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

这个项目里最有价值的结论都来自"先量后说"：散落点热图推翻了语义轮廓方案、DA3 输出探测推翻了透视反投影、点大小/帧率曲线暴露了填充率瓶颈。同时也要防**指标骗人**：用"非纸色像素占比"衡量覆盖率，结果被原图大块白色主导、四个点径几乎无差异（51.4%~51.9%），指标本身失效。规则：先确认指标能区分你要区分的东西，再拿它下结论。
