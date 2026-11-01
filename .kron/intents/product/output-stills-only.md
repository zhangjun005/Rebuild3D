<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-01T12:15:07.1971578Z
<!-- /kron:frontmatter -->
# output stills only

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

用户明确：GIF 与视频类型都不需要管，只输出图片。所以 WebM 录制、帧序列导出、无缝循环自检这一整套子系统全部移出范围，改为「当前画面导出 PNG」。这同时简化了很多：capture.ts 里约 400 行的录制/回绕/接缝自检代码可以删掉，循环对齐算式也不再需要。
