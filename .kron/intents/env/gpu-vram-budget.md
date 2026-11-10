<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:56.9680823Z
<!-- /kron:frontmatter -->
# gpu vram budget

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

8 GB 显存是硬约束，process_res 直接换成显存与耗时（实测 504→2160MiB/0.91s、1008→3027MiB/2.25s、1344→3919MiB/3.72s、1792→5519MiB/10.99s、2240→7577MiB 已贴 8G 上限、2688→10090MiB 溢出到内存）。当前默认 1008 + DA3 常驻 1276MiB，再叠加 onnxruntime CPU 的分割/补洞不占显存。提分辨率前必须先算显存。
