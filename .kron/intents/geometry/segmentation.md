<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-11T05:20:40.7918397Z
assumptions:
    - id: anime-seg-covers-all-illustrations
      severity: hard
      rationale: 分割链路的失败兜底就是为这条（已过期）的假设准备的；复核这条时若它变成真，退化逻辑就可以简化。
status: active
<!-- /kron:frontmatter -->
# 角色分割链路（预留，未用于渲染）

> 为"角色/背景分类"接了 anime-seg（skytnt/anime-seg, isnet_is ONNX, Apache-2.0, 168M…

B, onnxruntime CPU 0.65s/张，不占显存）。链路已完整打通：sidecar CMD_SEGMENT（cmd=4）+ Go 协议/客户端/管理器（含失步重建）+ /api/infer best-effort 调用 + meta 回显。但**尚未用于渲染**：按语义轮廓划边界带的方案已被证伪（散落点在线稿上，不在外轮廓）。实测 6/7 张可用、1 张浅色主体糊到边失败（可用"四边被判角色>50%"检测并退化）。原窗口不依赖它，保留给将来做视差分层。
