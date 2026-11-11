<!-- kron:frontmatter -->
id: lama-cpu-speed-acceptable
text: LaMa（isnet 补洞）在 CPU 上单张约 8.9 秒，可以接受作为每次推理的一部分。
default_severity: soft
status: active
created_by: '@zhangjun005'
updated_at: "2026-11-11T05:19:22Z"
expires_at: "2026-10-15"
<!-- /kron:frontmatter -->

## 这是待验证的假设，且已到期

测得：LaMa fp32 ONNX 在 onnxruntime **CPU** 上补一张 4000×4000 插画约 **8.9 秒**。

## 为什么它很可能是错的

端到端现在是 2.5 秒（推理 1.8 + 几何 0.6）。若把补洞塞进主链路会变成 ~11 秒，
与"不给电脑加压"的产品约束直接冲突。

## 两种出口

1. 装 `onnxruntime-gpu`（约 0.3 秒级）→ 假设成立，可以接进主链路
2. 改成"一次性生成 + 缓存"（不进每次请求）→ 也成立

在验证出口之前，补洞功能保持**未接入**（`sidecar/inpaint_engine.py` 存在但无人调用）。

> 这条**故意到期**：它现在明确不可用，到期就是提醒使用者"别直接接进主链路"。
