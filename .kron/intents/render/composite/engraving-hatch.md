<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:59:18.526562Z
<!-- /kron:frontmatter -->
# engraving hatch

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

铜版排线同样在合成 pass：按亮度/墨量切成交叉排线层数（hatchLayers / hatchSpacing / hatchAngle），再用纸色↔墨色双色调着色。它与半调网点是**互斥**的三选一（排线 > 网点 > 点云自带点画），因为两者叠加会变成噪声。engraving.frag.glsl。
