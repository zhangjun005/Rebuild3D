<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:57.1993599Z
<!-- /kron:frontmatter -->
# diagnostic discipline

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

自己写的 diag 脚本差点得出反向结论：等"点数 > 200k"时点云已经是 1M，条件立刻满足 → 在重新生成之前就截了图 → 两张图的标签是错的，差点报告"focal 3.0 是极端特写"。规则：轮询条件必须描述**本次操作引起的变化**（例：等状态文字从"推理中"回到"点云就绪"），而不是任何可能已经成立的绝对量。同理，验证前先看一眼"这个条件现在是不是已经为真"。
