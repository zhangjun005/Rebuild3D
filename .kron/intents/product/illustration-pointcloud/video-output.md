<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-11T05:19:29.1192495Z
status: superseded
references:
    - product/illustration-pointcloud/output-stills-only
<!-- /kron:frontmatter -->
# GIF / 视频输出（已废弃）

> 初版范围里包含"循环 WebM/GIF 导出"和"可交互网页"，理由是"能动的更有卖点"。

**已废弃的早期产品决策（superseded）。** 初版范围里包含"循环 WebM/GIF 导出"和"可交互网页"，理由是"能动的更有卖点"。后来砍掉：①用户明确"gif 和视频类型不需要管，我只想输出图片"；②视频编码在浏览器里要么引 ffmpeg.wasm（体积/内存），要么回 Go 侧（多一条编码链路），两者都是为了一个非核心功能加复杂度。被 `output-stills-only` 取代。web 侧只保留 exportPng。
