<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:57.618587Z
<!-- /kron:frontmatter -->
# 主动砍掉的范围

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

主动砍掉的东西，写下来避免反复讨论：① GIF/视频输出不做，只出静态图片；② 图像模式（原图直接风格化）降级为对照，产品主线是点云；③ 角色/背景分割暂时不用于渲染（原窗口不依赖它）；④ 补洞（LaMa）模型已下好、引擎已写，但**未接入**主链路（CPU 8.9s/张太慢，接入前需换 GPU provider 或改成一次性生成+缓存）。
