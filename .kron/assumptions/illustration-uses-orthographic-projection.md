<!-- kron:frontmatter -->
id: illustration-uses-orthographic-projection
text: 插画的点云反投影必须用正交（x 与深度无关），因为上游只给相对深度且没有相机内参；透视反投影等于假装存在一个不存在的相机。
default_severity: hard
status: active
created_by: '@zhangjun005'
updated_at: "2026-11-11T05:19:18Z"
verified_at: "2026-11-01"
verified_by: '@zhangjun005'
<!-- /kron:frontmatter -->

## 依据

模型侧：`depth` 无量纲、`is_metric` 空、`intrinsics`/`extrinsics` 为 None（见 `da3-depth-is-metric` 的推翻记录）。

渲染侧：透视相机会把远层再缩放一次。FOV 42° 时取景距离约 1.0 而点云 z 跨度 1.21，
远近层在画面上差约 2.1 倍 —— 这就是"以视点为中心的放射状扇形"的数学来源。
FOV 4° 差约 12%，FOV 1° 差约 3%。

## 实现

`internal/geometry/unproject.go` 的 `UnprojectBlend`：`perspective` 参数 0 = 纯正交（默认），
1 = 纯透视（保留作对照）。orthoScale 取 `dRef/focal`，使两者在代表深度处重合、切换时尺寸连续。

## 破裂条件

如果换成带真实内参的模型（DA3METRIC 之类），这条就该重估——那时"假装没有相机"反而会丢信息。
