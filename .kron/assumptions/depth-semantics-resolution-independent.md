<!-- kron:frontmatter -->
id: depth-semantics-resolution-independent
text: DA3MONO 的深度语义质量与 process_res 基本无关：分辨率从 504 提到 1344（像素 ×7.1），主体内结构（sub_iqr）只从 0.043 涨到 0.052（+19%），分离度从 0.764 到 0.802。提分辨率只加采样精度，不改深度语义。
default_severity: hard
status: active
created_by: '@zhangjun005'
updated_at: "2026-11-01T11:42:27Z"
<!-- /kron:frontmatter -->

## 实测（2026-11-01，同一张 平涂131905131_p0.png）

只改 `process_res`，看深度语义指标（`scripts/analyze-depth.py`）：

| process_res | 深度像素 | 主体内 IQR/全幅 | 主体vs背景分离度 |
|---|---|---|---|
| 504 | 254,016 | 0.0431 | 0.764 |
| 756 | 571,536 | 0.0461 | 0.783 |
| 1008 | 1,016,064 | 0.0434 | 0.791 |
| 1344 | 1,806,336 | 0.0515 (+19%) | 0.802 (+5%) |

**像素涨 7.1 倍，深度语义只改善 5~19%。**

## 推论

`process_res` 不是深度质量的杠杆，只是「采样精度」的杠杆。

所以要分开看：
- 想让深度**更准** → 换模型 / 换输入，提分辨率没用
- 想让点云**点数更多** → 提 `process_res` + `max_points`（见 `geometry/resolution-budget`）

## 破裂条件

如果换了模型（比如 DA3METRIC-LARGE）后发现 sub_iqr 对分辨率敏感，这条就破了。
到时候应重跑本实验而不是沿用结论。
