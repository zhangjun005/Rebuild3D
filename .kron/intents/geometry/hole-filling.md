<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-11T05:20:38.1190056Z
status: draft
assumptions:
    - id: lama-cpu-speed-acceptable
      severity: soft
      rationale: 这条假设直接决定补洞要不要接进主链路：它一旦为假（CPU 太慢），功能就只能停在离线。
<!-- /kron:frontmatter -->
# 补洞：LaMa 抠角色后补背景（草案）

> 分层视差的固有代价：角色后面的背景像素原图里不存在，层一错动就露空洞。

**草案（draft），引擎已写但未接入。** 分层视差的固有代价：角色后面的背景像素原图里不存在，层一错动就露空洞。方案是分割出角色→抠掉→用 LaMa 补出完整背景（`sidecar/inpaint_engine.py`，Carve/LaMa-ONNX，Apache-2.0，198MB）。**未接入的原因**：CPU 单张 8.9 秒（见 assumption `lama-cpu-speed-acceptable`），接进主链路会把端到端从 2.5s 推到 ~11s。出口是换 `onnxruntime-gpu` 或改成一次性生成+缓存。另有一个已修的坑：分割 mask 的三档阈值不适合未校准模型，会让 LaMa 把整图当洞填（见 `data/mask-thresholds`）。
