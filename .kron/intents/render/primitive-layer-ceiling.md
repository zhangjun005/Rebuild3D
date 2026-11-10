<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:59:38.081822Z
assumptions:
    - id: pointcloud-nonmonotonic-tuning
      severity: soft
      rationale: 点大小与软边都是非单调的：太小→画面变淡，太大→糊；硬边反而最淡（点缝全露纸色）。所以调参不能靠单调外推，必须成对看观感。
<!-- /kron:frontmatter -->
# primitive layer ceiling

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

当前 L1 基元只有一个各向同性软圆盘（length(gl_PointCoord-0.5) + smoothstep），没有法线、没有椭球、没有抗锯齿。这一层决定了点云渲染的上限，也是"变化最大、最便宜"的探索维度（只改片元 shader，不动数据）。放大 400% 可见：线条是阶梯状断续点、平坦区有散落白点、点径调大后填充率断崖（pointSize=8 → 35fps，原 144fps）。
