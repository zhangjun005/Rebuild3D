<!-- kron:frontmatter -->
id: da3-mono-fits-8gb
text: DA3MONO-LARGE（0.35B）在 process_res=504 单图推理时的峰值显存低于 6GB，可在 RTX 4060 Laptop 8GB 上稳定运行。
default_severity: hard
status: active
created_by: '@zhangjun005'
updated_at: "2026-11-01T08:48:06Z"
expires_at: "2026-12-31"
<!-- /kron:frontmatter -->

## 估算依据

0.35B 参数 fp32 权重约 1.4GB；DA3 的 forward 自带 torch.autocast（bf16/fp16），激活值随 504 分辨率增长。
估算峰值 2~3GB。但这是**估算，未实测**。

## 怎么验证

跑一次单图推理，打印 torch.cuda.max_memory_allocated()。

## 破裂时的应对

降 process_res（504 → 378）或退到 DA3-SMALL（0.08B）。
