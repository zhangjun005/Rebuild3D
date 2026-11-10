<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:57.4496953Z
<!-- /kron:frontmatter -->
# ILC1 点云格式：8+15n 字节，零拷贝直传

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

ILC1 是 Go→浏览器的唯一数据：magic "ILC1"(4B) + count(u32) + pos f32[3n] + col u8[3n]，共 8+15n 字节（100 万点 = 14.31 MB）。设计目标是零拷贝直传 geometry attribute，所以布局完全贴合 BufferAttribute 的 stride。任何扩展（法线/切向/层 id）都必须向后兼容——老文件仍要能加载，前端要按 magic + 长度校验拒绝坏数据（已实现：byteLength 为 0 / <8 / magic 错 / 8+count*15 ≠ n 都会显式报错）。
