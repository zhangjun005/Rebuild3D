<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:45.1450534Z
<!-- /kron:frontmatter -->
# canvas vs paper

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

点云渲染里其实有两个"背景"：纸色（点缝里透出来的纸，参与点的空隙/雾混色）和画布底色（整张画布在完全没有点处的颜色）。之前共用一个控件，想放到深色底上出图时必须牺牲纸感。现已拆成两个独立参数（bgColor / paper），6 个预设各自显式配对，保证现有观感像素级不变。记录一个反直觉现象：深色画布下点云看起来"有颗粒"不是 bug——那是点缝露出了画布底色，以前纸色=画布色所以看不见；想要实应该调点大小/密度，而不是改纸色。
