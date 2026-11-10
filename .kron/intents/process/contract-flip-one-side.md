<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:59:50.2107633Z
assumptions:
    - id: depth-sign-convention
      severity: hard
      rationale: 这个约定是“只能在一侧修”的对象：它同时约束 Go 的反投影和浏览器的相机，所以发现方向反了时必须先确认是哪一侧错了，而不是两边都改。
<!-- /kron:frontmatter -->
# 跨边界约定只能翻一侧

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

相机约定（+x 右 / +y 上 / +z 朝观察者 / z=-d）曾经在 Go 和渲染器两边各翻一次，等价于没改，症状是"空气透视把主体洗白、按深度上墨落到背景、视差反向"，排查了很久。规则：跨边界的符号约定**只能在一侧修正**，改完必须用一条数值断言锁住（现在 docs/selection.md §4.4 里有 ⚠️ 不要改回去 的警告块）。
