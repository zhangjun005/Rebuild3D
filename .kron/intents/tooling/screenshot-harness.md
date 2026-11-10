<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:57.1229049Z
<!-- /kron:frontmatter -->
# shot.cjs：唯一取证工具

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

Playwright MCP 在本机不可用（它去找 Chrome，本机只有 Edge）。所以自建 scripts/shot.cjs：require 本地 playwright + channel='msedge'，支持 --ilcl 直接加载点云文件（绕过 Go）、--preset/--param 覆盖参数、--fov 改相机、--angle 绕目标旋转、--no-panel 裁掉面板、--clip 局部截图。它是所有渲染实验的唯一取证工具，能力要持续补（不是一次性的脚本）。
