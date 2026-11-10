<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-01T12:15:07.2777804Z
<!-- /kron:frontmatter -->
# 工作分辨率与 max_points 解耦

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

结构采样目前 workRes = sqrt(max_points*3)，即工作网格分辨率被点数预算绑死。但两者的意义完全不同：workRes 决定「线稿最细能到多少」，max_points 决定「保留多少个点」。要渲染的点云里保留线稿，必须让 workRes 独立于 max_points（例如提到 2048，候选网格 420 万，仍只选 20~60 万点）。
