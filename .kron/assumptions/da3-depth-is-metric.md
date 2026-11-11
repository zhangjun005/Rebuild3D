<!-- kron:frontmatter -->
id: da3-depth-is-metric
text: DA3MONO 输出的 depth 可以直接当米制距离用，配一个按长边估的 focal_factor 就能做透视反投影 x=(u-cx)·d/f。
default_severity: hard
status: superseded
created_by: '@zhangjun005'
updated_at: "2026-11-11T05:19:18Z"
verified_at: "2026-11-01"
verified_by: '@zhangjun005'
<!-- /kron:frontmatter -->

## 这是早期的（错误）判断，已 superseded

项目初期按「单目深度 ≈ 相对可靠的米制距离」来设计，于是：

- 反投影用透视公式 `x = (u-cx) · d / f`，f 取 `focal_factor × max(W,H)` 估出来
- 认为"远层被放大"是透视的正常表现

## 为什么错了（2026-11-01 实测）

探 `pred` 的实际字段：

| 字段 | 值 |
|---|---|
| `depth` | 范围 [0.059, 1.271]，**无量纲** |
| `is_metric` | 空 dict（不是度量深度） |
| `scale_factor` | None |
| `intrinsics` / `extrinsics` | **None** |

所以 `d/f` 里两个量都是假设：d 无物理单位、f 是猜的。
"远层被放大"是这个假设的产物，不是模型信息。

## 被谁取代

`illustration-uses-orthographic-projection` —— 改为正交反投影 `x=(u-cx)·s`，
其中 s 是唯一的自由参数，正好匹配"相对深度"这个事实。

## 留着它的意义

这是本项目最大的一次方向修正。留着它，才能在半年后看懂
「为什么代码里同时有 `UnprojectPerspective` 和 `UnprojectBlend`，而后者才是默认」。
