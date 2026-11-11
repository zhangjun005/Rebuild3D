<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-11T05:19:32.5567232Z
assumptions:
    - id: illustration-uses-orthographic-projection
      severity: hard
      rationale: 这是本 intent 的核心前提：选用正交不是因为风格偏好，而是因为上游深度无内参无尺度，透视反投影会把两个猜测相乘。
status: active
<!-- /kron:frontmatter -->
# 正交反投影：没有内参就不假装有相机

> 因为模型没有内参、深度是相对的，透视反投影是"假装有一个不存在的相机"。

正交投影 x=(u-cx)·s、z=-d 里 s 是唯一的自由参数，正好匹配这个事实。实现为可混合形式（perspective=0 纯正交 / 1 纯透视），orthoScale 取 dRef/focal 使两者在代表深度处重合，切换时尺寸连续。验证：正交下 x 跨度 0.702 与深度无关，z 跨度可独立压缩。
