<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-01T13:53:58.0901473Z
status: active
<!-- /kron:frontmatter -->
# 渲染方法的论文出处，以及哪些该采纳

> > 用户追问：这些渲染方法里，有几个看起来是已经做成工程/论文的？

> 确实大半有出处。本文记录出处 + **为什么不直接套用** + **哪一个该采纳**。

## 出处清单（`docs/rendering-styles.md §10` 有链接）

| 方法 | 出处 | 我们实现了吗 |
|---|---|---|
| **EWA splatting / surfel 圆盘** | Zwicker et al., TVCG 2002；Pfister《Surfels》SIGGRAPH 2000 | ❌ 只有软边圆片 |
| **加权 Voronoi 点画** | Secord《Weighted Voronoi Stippling》NPAR 2002 | ❌ 改用逐点哈希抖动 |
| **点云交互式点画** | Interactive Stipple Rendering for Point Clouds (ZCU) | ❌ |
| **点云排线** | Art-directed Hatching of 3D Point Clouds（扩展 X-Toon） | ⚠️ 简化版（屏幕空间排线） |
| **笔触化 splat** | Stroke-based splatting, The Visual Computer 2017 | ❌ |
| **描边** | Suggestive Contours (DeCarlo, SIGGRAPH 2003)；Hertzmann《Line drawings from 3D models》 | ⚠️ 简化版（深度 sobel） |
| **网点** | Ulichney《Digital Halftoning》1987；void-and-cluster | ✅ blue noise 用 void-and-cluster 生成 |
| **高斯风格化** | Gaussian Splatting in Style (arXiv 2403.08498)；GAGS | ❌ |

## 为什么大部分不能直接用

| 方法 | 障碍 |
|---|---|
| **Voronoi 点画** | CPU 迭代，离线生成要几秒~几十秒，且产出静态图案。我们的点云每换一张图就变，还要能实时调参 |
| **Suggestive Contours** | 需要**网格 + 视图相关曲率导数**。我们只有深度图，没有网格和法线场 |
| **3DGS 风格化** | 需要训练/优化大量高斯，几十分钟起。我们是"拖进去 2 秒出图" |
| **笔画 splatting** | 需要笔触方向场（通常从网格曲率算） |

**共性：这些方法假设输入是「带法线的密集通用点云」，而我们是「单张插画反投影出的 2.5D 浮雕 + 每点颜色」。** 约束不同。

## ⭐ 该采纳的：surfel / EWA splatting

**它正好解决当前最痛的问题：点之间露纸色导致发白。**

现在的做法是**加大点径**去盖缝，这是 splatting 的穷人版，有三个毛病：
1. 换点数时要重算点径（屏幕点间距变了）
2. 点径是屏幕像素 → **旋转/缩放时覆盖会变**
3. 点大了线就糊（实际撞到过这个，见 `pointcloud-nonmonotonic-tuning`）

**真正的 splatting**：每个点带**世界空间半径**，投影到屏幕：

```
屏幕半径 = 世界半径 × (屏幕高度/2) / (tan(fov/2) × 点深度)
```

好处：
- **表面覆盖率恒定**，与距离/角度/点数无关 → 不再需要手调点径
- 点大小自动随深度变化（近大远小），符合透视
- 缝隙问题从根上解决，而不是靠"把点吹大"掩盖

代价：要算每点半径（可从采样间距推），要做各向异性需 EWA + 法线。

## 结论

**大多数论文方法我们现在还没有撞到它们要解决的问题；唯一值得立刻采纳的是 splatting。**
采纳后「点大小」参数可能退化为「半径倍率」。

## 什么情况下应回头采用其他方法

- 如果要做**大幅旋转/环绕** → Suggestive Contours 类视角相关描边会明显优于深度 sobel
- 如果要做**离线高质量导出**（可以等几十秒）→ Voronoi 点画值得试
- 如果要**通用 3D 点云输入**（扫描件）→ 整套论文方法都适用
