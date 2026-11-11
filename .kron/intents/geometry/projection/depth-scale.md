<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:35.1525959Z
status: active
<!-- /kron:frontmatter -->
# z 轴压缩：正交下的浮雕厚度

> 正交下 z 只影响"浮雕厚度"，可以独立压缩：z = -RefDepth - (d-RefDepth)·DepthScale。

公式刻意设计成 DepthScale=1 时退化为 z=-d（保住 docs/selection.md §4.4 的旧约定与全部既有测试），DepthScale=0 时贴在 z=-RefDepth 的平面上。实测 z 跨度 1.212→0.606→0.242 而 x 跨度恒为 0.702。副作用是好的：层间视差变弱、侧转露出的背景空洞变小，30° 侧视从"散开拉丝"变成"轻微错动"。
