<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:45.057907Z
<!-- /kron:frontmatter -->
# 点云渲染范式清单与完成情况表

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

目标是**探索多种点云渲染范式**，不是把画质修得更像原图。所以先把可尝试的方式一次列全、统一编号，再用状态列跟踪进度。docs/rendering-methods.md 收了 7 层共 55 项（A 基元/笔触、B 混合叠加、C 颜色映射、D 世界空间图案、E 合成后期、F 运动交互、G 数据侧），当前 22 已实现 / 5 部分 / 28 未做。表里有验收标准（例：D 层必须用两帧对比证明图案随表面走）与实验登记表，实现一项就改状态并登记。
