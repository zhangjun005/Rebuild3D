<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:57.0450493Z
<!-- /kron:frontmatter -->
# model cache offline

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

DA3 权重走本地快照（snapshot_download local_files_only），不联网也能跑；sidecar 的 local_files_only 由 --allow-network 控制。分割与补洞模型直接放 E:\models 下用路径引用，不进 HF 缓存。这样断网、换机器、清缓存都不会让推理链路失效——代价是要记清楚每个模型的落盘位置。
