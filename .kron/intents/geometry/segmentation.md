<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:45.5601244Z
<!-- /kron:frontmatter -->
# 角色分割链路（预留，未用于渲染）

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

为"角色/背景分类"接了 anime-seg（skytnt/anime-seg, isnet_is ONNX, Apache-2.0, 168MB, onnxruntime CPU 0.65s/张，不占显存）。链路已完整打通：sidecar CMD_SEGMENT（cmd=4）+ Go 协议/客户端/管理器（含失步重建）+ /api/infer best-effort 调用 + meta 回显。但**尚未用于渲染**：按语义轮廓划边界带的方案已被证伪（散落点在线稿上，不在外轮廓）。实测 6/7 张可用、1 张浅色主体糊到边失败（可用"四边被判角色>50%"检测并退化）。原窗口不依赖它，保留给将来做视差分层。
