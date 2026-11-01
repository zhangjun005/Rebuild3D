<!-- kron:frontmatter -->
id: da3-mono-fits-8gb
text: DA3MONO-LARGE（0.35B）在 process_res=504 单图推理时的峰值显存低于 6GB，可在 RTX 4060 Laptop 8GB 上稳定运行。
default_severity: hard
status: active
created_by: '@zhangjun005'
updated_at: "2026-11-01T10:44:12Z"
expires_at: "2026-12-31"
verified_at: "2026-11-01"
verified_by: '@zhangjun005'
<!-- /kron:frontmatter -->

## 实测结果（2026-11-01，RTX 4060 Laptop 8GB）

**通过。峰值显存 2157 MiB**（目标 < 6144 MiB），余量充足。

| 项 | 实测 |
|---|---|
| 峰值显存 | 2157 MiB（7 张图取最大值） |
| 单图推理耗时 | 0.15 ~ 0.23 s（预热后） |
| 输入分辨率 | process_res=504，长边缩放 |
| NaN 比例 | 0% |

结论：8GB 上完全够用，甚至可以用更大的 process_res 换细节。

附带发现：DA3MONO-LARGE **不输出 conf 置信度图**，也不输出 intrinsics/extrinsics
（单目模型，无位姿）。置信度过滤方案需改为深度梯度/边缘替代。
