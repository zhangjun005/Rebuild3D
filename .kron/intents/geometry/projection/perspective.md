<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-11T05:19:32.6487318Z
status: superseded
assumptions:
    - id: da3-depth-is-metric
      severity: hard
      rationale: 这条 superseded 的假设是透视方案当初成立的唯一依据；把它挂在这里，改动正交公式前必须看到它已经被推翻。
references:
    - geometry/projection/orthographic
<!-- /kron:frontmatter -->
# 透视反投影（已废弃）

> 项目初期用透视反投影 x=(u-cx)·d/f，f 由 focal_factor × 长边估算，理由是"单目深度大致能当米制距离"。

**已废弃的原始决策（superseded）。** 项目初期用透视反投影 x=(u-cx)·d/f，f 由 focal_factor × 长边估算，理由是"单目深度大致能当米制距离"。实测模型输出后这条被推翻：depth 无量纲、is_metric 空、intrinsics/extrinsics 为 None，所以 d/f 里两个量都是假设，"远层被放大"是这个假设的产物。被 `geometry/projection/orthographic` 取代。代码里 `UnprojectPerspective` 保留作对照，默认路径已改走 `UnprojectBlend(perspective=0)`。
