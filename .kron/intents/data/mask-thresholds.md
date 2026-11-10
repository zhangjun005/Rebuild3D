<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:57.5389946Z
<!-- /kron:frontmatter -->
# mask thresholds

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

分割 mask 最初做成三档（0/128/255，用 0.4/0.6 分界），实测这个模型的输出概率**未校准**：背景常落在 0.4~0.6，于是整片背景被判成"不确定"，下游 LaMa 把整张图当洞 → 补出一片白。教训：对未校准的概率输出不要用窄带三档阈值，应该先量分布再定阈值（或直接二值 0.5）。
