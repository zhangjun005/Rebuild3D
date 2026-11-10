<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:59:18.4468451Z
<!-- /kron:frontmatter -->
# halftone screen

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

半调网点**必须放在合成 pass**，不能放在点里：如果按点着色，网点会跟着点走，转动时图案在物体上滑动（错）。放屏幕空间后，图案固定在画面上，物体在图案下移动——这才是印刷的观感。代价是它不是"贴在世界表面上"的网点（那是 D2，未做）。实现：halftone.frag.glsl，参数 halftone / halftoneCell / halftoneAngle。
