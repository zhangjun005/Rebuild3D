<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:35.2308017Z
<!-- /kron:frontmatter -->
# seam compression iqr

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

把散落点（深度落在层间的点）反投影回图像坐标做热图，发现它们**恰好描在插画的每根线稿上**——发丝、眼睛、衣褶、指甲，全图都是，而不是集中在人物外轮廓。这推翻了"按语义轮廓划边界带"的方案（实测只覆盖外轮廓、只改 1.3% 像素、侧视无变化）。正确做法是按深度不连续划带，并用**同层 IQR 判据**区分：突变两侧稳定深度 IQR 小 = 线稿缝合伪影（压缩）；IQR 大 = 真实台阶（如手臂朝前），保持不动。代码在 internal/pipeline/band.go，默认关闭待验证。
