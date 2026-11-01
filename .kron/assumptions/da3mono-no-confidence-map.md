<!-- kron:frontmatter -->
id: da3mono-no-confidence-map
text: DA3MONO-LARGE 的 Prediction 不带 conf / intrinsics / extrinsics；置信度过滤方案必须改用深度梯度与边缘替代，焦距只能假设（对插画线是艺术参数，可接受）。
default_severity: hard
status: active
created_by: '@zhangjun005'
updated_at: "2026-11-01T10:44:12Z"
<!-- /kron:frontmatter -->

## 依据（2026-11-01 实测）

`DepthAnything3.from_pretrained("depth-anything/DA3MONO-LARGE").inference([img])` 返回的
`Prediction` 里：

- `depth` ✅ 有
- `conf` ❌ **无**（`getattr(pred, "conf", None)` 为 None）
- `intrinsics` ❌ 无
- `extrinsics` ❌ 无

## 影响

原设计里"按 conf 分位数过滤飞点"这一招**用不了**，必须换方案：

1. **深度梯度过滤**：相邻像素深度跳变超阈值即判为遮挡边界，两侧剔除
   （Go 侧 `geometry.FilterDepthEdges` 已经实现了这个，正好替代）
2. **边缘 + 面积过滤**：极小连通块的孤立点直接丢弃
3. 不再有"置信度热力图"这个可视化，改为深度梯度热力图

## 另外

没有 `intrinsics` 意味着**焦距必须假设**。这不影响插画线：
`selection.md` 已把焦距定义为**艺术参数**（广角→点云厚、长焦→薄），不需要真实值。
