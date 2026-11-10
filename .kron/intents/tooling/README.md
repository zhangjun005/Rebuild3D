<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:34.7124886Z
<!-- /kron:frontmatter -->
# tooling

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

本项目的验证几乎全靠"自动截图 + 数值对比"，而不是靠肉眼看窗口。工具链本身踩过坑（Playwright MCP 找不到 Chrome、轮询条件写错导致截图早于操作完成、PowerShell 引号吃掉 JSON），这些经验值得单独成域记录，否则每次都要重新踩。
