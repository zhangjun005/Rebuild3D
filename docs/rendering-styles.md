# 渲染风格清单

<!-- @kron:intent render/style-layering -->
<!-- @kron:intent render/style-layering/primitive-layer -->

> **目的：固化下来以免后面忘记。** 这是"插画点云风格渲染"这条线的**价值核心**——数据管线是苦活，风格才是卖点。
> 补：本清单只列手段，不列取舍。取舍见 [scope.md](scope.md) 阶段 P0/P3。

---

## 0. 三层结构（总览）

风格化不是一个决定，是三个正交的决定：

```
L1 基元层   一个点被画成什么形状？    圆点 / 软片 / EWA 圆盘 / 笔触 / 粒子
L2 风格层   整个画面是什么画风？      点画 / 排线 / 半调 / 描边 / 版画 / 像素 / 笔触
L3 时间层   动起来是什么质感？        沸腾线 / 抖动换序 / 笔触跟随 / 漂浮
     ─────────────────────────────
L4 后期层   通用增强（与风格正交）    空气透视 / 辉光 / 景深 / 色调映射 / 颗粒
```

**可以自由组合。** 例如：「软片基元 + 点画风格 + 沸腾线 + 空气透视」= 手绘点彩动画。

---

## 1. L1 基元层：五种

| 基元 | 实现 | 点数上限 | 各向异性 | 备注 |
|---|---|---|---|---|
| **A. `THREE.Points` 圆点** | `PointsMaterial` | ~100 万 | ❌ | 最便宜，1 个 draw call。`gl_PointSize` 有驱动上限（常见 64~255） |
| **B. 软边圆片** ⭐默认 | `ShaderMaterial` + `gl_PointCoord` | ~100 万 | ❌ | 比 A 只多一点成本，观感提升最大 |
| **C. EWA splat / surfel 圆盘** | 每点带法线+半径，投影成椭圆 | ~10 万 | ✅ | 高斯之前的主流做法。"连续表面"感最好 |
| **D. 各向异性 quad** | `InstancedMesh` + 自定义 quad | ~10 万 | ✅ | **能贴纹理**（笔刷/网点），能任意朝向。笔触感必须用这个 |
| **E. 粒子（加法混合）** | `AdditiveBlending` + `depthWrite:false` | ~50 万 | ❌ | 发光/星云感 |

**B 的核心 GLSL**（`fragment`）：

```glsl
vec2 c = gl_PointCoord - 0.5;
float r = length(c);
if (r > 0.5) discard;
float alpha = smoothstep(0.5, 0.30, r);   // 软边
```

**B 的 `vertex`**（深度衰减点大小，见 L4）：

```glsl
gl_PointSize = uPointSize * uScale / -mvPosition.z;
```

---

## 2. L2 风格层：七种

### 2.1 点画 Stippling ⭐（点云最契合的风格）

**核心思路：用点密度表达明暗**，而不是用颜色。暗处点密、亮处点疏。

| 实现路线 | 做法 | 成本 | 效果 |
|---|---|---|---|
| **屏幕空间抖动**（快速原型） | blue noise 纹理阈值 + luminance → discard | ~10 行 | 立刻得到点画感，但点是"屏幕像素"不是"3D 点" |
| **逐点抖动** ✅ **当前实现** | 逐点哈希决定去留，保留概率 = 1 − 亮度 | ~30 行 | **3D 稳定、转相机不游走、无摩尔纹** |
| **预处理重采样** | CPU 侧按明暗决定采样密度 | ~80 行 | 最正宗，点数可控 |

> ### 实现决定（2026-11 实测）：默认走「逐点抖动」，不用屏幕空间 blue noise
>
> **原计划**是先上前两行里的"屏幕空间抖动"（下面这段 10 行起手式），跑通再考虑升级。
> **实测发现它在本项目里不可用**：屏幕空间 blue noise 阈值去打一个**规则点阵**
> （测试球面是 Fibonacci 点阵，点间距约 5px，blue noise cell 2.5px）会**拍频出摩尔纹**——
> 整片球面被纹上一圈圈斜向假纹理，疏密关系全乱。这是采样定理问题，不是调参能解决的。
>
> **于是改走「逐点抖动」**：每点用自己的确定性哈希决定去留，保留概率 = `1 − 亮度`。
> 收益：3D 稳定、转相机不游走、无摩尔纹、且**密度真正成为点云自身的属性**（更贴合 §5
> "点密度即风格参数"）。代价：低密度时哈希有轻微团簇（真实深度点云比规则点阵轻）。
>
> blue noise 纹理没有废弃，改给 **L4 纸颗粒**用，后续 §2.4 半调 / §3 抖动换序也可复用同一张。
>
> 下面这段 10 行起手式**保留作快速原型参考**，不要当默认实现。

**屏幕空间的 10 行起手式**（仅作原型，详见 §8）：

```glsl
float n = texture2D(uBlueNoise, gl_FragCoord.xy / 256.0).r;
float lum = dot(color, vec3(0.299, 0.587, 0.114));
if (n > lum) discard;
```

参数：`stippleScale`（网点大小）、`densityGamma`（明暗→密度曲线）

参考：[Interactive Stipple Rendering for Point Clouds](https://dspace.zcu.cz/bitstreams/386666f4-28c6-4532-b560-59da98ecee69/download)；经典算法 Weighted Voronoi Stippling（Secord, NPAR 2002）

---

### 2.2 排线 Hatching / 交叉排线

按明暗铺 2~4 层排线，越暗层数越多。

| 路线 | 做法 |
|---|---|
| 屏幕空间 | 预生成 3~4 张不同角度的排线纹理，按亮度阈值叠加 |
| 世界空间（X-Toon 思路） | 把明暗映射到纹理坐标 `v`，一张排线纹理按 `v` 采样 → 明暗变化时排线自然过渡 |

参数：`hatchLayers`(2~4)、`hatchAngle`（每层 +30°）、`hatchSpacing`、`hatchThresholds`

参考：[A Non-Photorealistic Rendering Technique for Art-directed Hatching of 3D Point Clouds](https://pdfs.semanticscholar.org/5052/b24ab920aa84a47755481c17b9ee61b940c8.pdf)（扩展 X-Toon）

---

### 2.3 笔触基元 Stroke-based

每个点渲染成一条**短笔触**，朝向由主方向决定（法线切向 / 图像梯度 / 相机 up）。

- 必须用 `InstancedMesh` + quad（基元 D）
- quad 贴一张笔刷 alpha 纹理
- 朝向以指数平滑跟随，避免抖动

参数：`strokeLength`、`strokeWidth`、`strokeJitter`（长度随机化）、`strokeFollowSpeed`

参考：[Stroke-based splatting](https://link.springer.com/article/10.1007/s00371-017-1420-7)（The Visual Computer 2017，直接把 splat 画成笔触）

---

### 2.4 半调 / 网点 Halftone

旋转网格，每格内画圆，半径 ∝ 明暗。

```glsl
vec2 g = rot(fragCoord, uScreenAngle);
vec2 cell = fract(g / uCellSize) - 0.5;
float r = sqrt(1.0 - lum) * 0.7;
if (length(cell) > r) discard;
```

参数：`screenAngle`（印刷常用 15° / 45° / 75°）、`cellSize`

---

### 2.5 描边 Toon / Contour

需要 **depth buffer + normal buffer**（屏幕空间后处理）。

```
edge = sobel(depth) > t1  ||  sobel(normal) > t2
```

更讲究的做法（线描理论）：
- **轮廓线**：视线方向 · 法线 ≈ 0
- **褶线**：主曲率极值
- **暗示线 suggestive contours**：视线掠过表面的临界处 → 内轮廓，最像手绘

参数：`edgeThreshold`、`edgeWidth`、`edgeColor`、`edgeJitter`（手绘不规则）

参考：Hertzmann《Line drawings from 3D models》；[Suggestive Contours](https://gfx.cs.princeton.edu/pubs/DeCarlo_2003_SCF/)（DeCarlo, SIGGRAPH 2003）

---

### 2.6 版画 / 丝网印

```
color = quantize(color, levels)                  // 色阶分层
color = mix(uInkA, uInkB, lum)                   // 双色映射
color += orderedDither(fragCoord) * uDitherAmt   // 有序抖动
```

参数：`levels`(2~6)、`inkA`、`inkB`、`paperColor`、`ditherAmt`

---

### 2.7 像素 / voxel

| 路线 | 做法 | 观感 |
|---|---|---|
| 屏幕空间像素化 | 低分辨率 render target + `NearestFilter` 放大 | 复古像素 |
| 世界空间 voxel | 点云 voxel 化 → `InstancedMesh` 方块 | 乐高/我的世界 |

参数：`pixelSize` 或 `voxelSize`

---

## 3. L3 时间层：动起来才有意义

| 手法 | 做法 | 效果 |
|---|---|---|
| **沸腾线 boiling** ⭐ | 每 2~3 帧重算抖动/笔触相位（固定 seed 序列） | **手绘动画感，插画动图最有效的一招** |
| 抖动换序 | blue noise 纹理按帧偏移 UV | 复古印刷的动态 |
| 笔触跟随 | 笔触朝向 lerp 到相机运动方向 | 油画跟手感 |
| 漂浮漂移 | 点位置加极小 sin 噪声 | 梦幻感（**会破坏几何，谨慎**） |

**沸腾线的实现要点**：

```glsl
float seed = floor(uTime * uBoilRate);                       // 每秒换几次
vec2 jitter = hash2(seed) * uJitterAmt;                       // 每帧偏移
float n = texture2D(uBlueNoise, gl_FragCoord.xy / 256.0 + jitter).r;
```

**GIF 循环时**：`uTime` 必须按 `frame % cycle` 计算，否则循环点会跳。

---

## 4. L4 后期层：与风格正交的通用增强

| 效果 | 做法 | 备注 |
|---|---|---|
| **空气透视** ⭐ | `color = mix(color, uFogColor, smoothstep(near, far, depth))` | **单点最有效的一招**，立刻有空间感 |
| 深度衰减点大小 | `gl_PointSize *= 1.0 / -mvPosition.z` | 与空气透视联动最强 |
| 辉光 bloom | 亮部阈值提取 + 模糊叠加 | 梦幻/夜景 |
| 景深 bokeh | 远处点变大变虚（点云天然适合，不需要额外 pass） | 微缩模型感 |
| 暗角 vignette | 到屏幕中心距离衰减 | 聚焦 |
| 色调映射 | ACES / Reinhard / 自定义 S 曲线 | 压高光 |
| 胶片颗粒 | 屏幕空间 noise | 质感 |

---

## 5. 点云独有的一件特权

> **点密度本身就是风格参数。**

用密度表达明暗（点画本质）。这在 mesh 上做不到——mesh 的采样密度由拓扑决定。

三种做法：
1. **减少点数**（预处理）—— 最正宗，但不可实时调
2. **每点透明度**—— 最简单，实时可调
3. **每点大小**—— 视觉上近似密度

**所以"点数"在这条线上是要主动控制的美术量，不是要最大化的性能指标。**（对比：真实重建里点数是"越多越好"）

---

## 6. 参数清单（做 UI 用）

| 层 | 参数 | 类型 | 范围/取值 |
|---|---|---|---|
| L1 | `basePrimitive` | enum | 圆点 / 软片 / 圆盘 / quad / 粒子 |
| L1 | `pointSize` | float | 0.5 ~ 10 |
| L1 | `softness` | float | 0 ~ 1 |
| L1 | `strokeLength` / `strokeWidth` | float | 0 ~ 20 / 0 ~ 5 |
| L2 | `stylePreset` | enum | 点画 / 排线 / 半调 / 描边 / 版画 / 像素 / 笔触 |
| L2 | `stippleScale` / `densityGamma` | float | 0.5~4 / 0.5~2 |
| L2 | `hatchLayers` / `hatchAngle` / `hatchSpacing` | int / deg / px | 2~4 / 0~180 / 2~12 |
| L2 | `screenAngle` / `cellSize`（半调） | deg / px | 15/45/75 / 2~12 |
| L2 | `levels`（色阶） | int | 2 ~ 8 |
| L2 | `inkA` / `inkB` / `paperColor` | color | — |
| L2 | `edgeThreshold` / `edgeWidth` / `edgeJitter` | float | — |
| L3 | `boilRate` / `jitterAmt` | float | 0（关）~ 4 / 0 ~ 2 |
| L4 | `fogColor` / `fogNear` / `fogFar` | color / float | — |
| L4 | `bloomStrength` / `vignette` / `grain` | float | 0 ~ 2 / 0 ~ 1 / 0 ~ 1 |
| — | `densityScale` | float | 0.1 ~ 1（降采样比例 = 美术量） |
| — | `projection` | enum | `perspective` / `orthographic` |
| — | `fovDeg` | float | 20 ~ 90（**艺术参数，不是相机参数**） |

---

## 7. 六套预设（组合好直接用）

| 预设 | L1 基元 | L2 风格 | L3 时间 | L4 后期 | 观感 |
|---|---|---|---|---|---|
| **点画印刷** `stipple-print` | 软片 | 点画（屏幕抖动） | 沸腾线 | 空气透视 + 单色纸墨 | 手绘点彩 |
| **铜版画** `engraving` | 软片 | 排线(3层) + posterize(3) | 无 | duotone + 纸黄 | 蚀刻/版画 |
| **半调漫画** `manga` | 软片 | 半调 + 粗描边 + 高对比 | 无 | 无 | 黑白漫画 |
| **油画** `painterly` | 笔触 quad | 笔触 + 暖色 | 笔触跟随 | 轻微 bloom | 厚涂油画 |
| **像素立体书** `pixel-diorama` | voxel 方块 | 像素化 + posterize(6) | 无 | 硬阴影感 | 像素微缩 |
| **梦境星空** `dreamy` | 粒子（加法） | 无 | 缓慢漂移 | 强 bloom + 深蓝背景 + 景深 | 星云/梦 |

**推荐首发组合（P0 要做的）**：

```
点画印刷：软片基元 + blue noise 点画 + 深度描边 + 空气透视 + 沸腾线
```

理由：全部是 shader，约 200 行，**不需要任何模型**，观感提升最大。

---

## 8. 起手式：blue noise 点画（约 10 行）—— **仅原型，非默认实现**

> ⚠️ **2026-11 实测结论：这一招在本项目里会摩尔纹，不要当默认实现。**
> 屏幕空间 blue noise 阈值去打规则点阵（Fibonacci 球面）会拍频出斜向假纹理。
> 默认实现已改为 **逐点哈希抖动**（保留概率 = 1 − 亮度），详见 §2.1 的「实现决定」框。
> 下面这段保留作快速原型 / 对照参考，blue noise 纹理现在给 L4 纸颗粒用。

```glsl
uniform sampler2D uBlueNoise;   // 一张 256×256 的 blue noise 纹理
uniform float uScale;           // 网点大小
uniform float uGamma;           // 密度曲线

float n = texture2D(uBlueNoise, gl_FragCoord.xy / (256.0 * uScale)).r;
float lum = pow(dot(vColor, vec3(0.299, 0.587, 0.114)), uGamma);
if (n > lum) discard;
```

**blue noise 纹理怎么来**：一张 256×256 的 PNG 就够。`web/scripts/gen-blue-noise.mjs` 已实现
void-and-cluster 生成（低频能量约为白噪声的 0.39×，生成约 6 秒）。

---

## 9. 实现注意与坑

| 坑 | 说明 | 对策 |
|---|---|---|
| `gl_PointSize` 上限 | 驱动相关，常见 64~255；超过就裁剪 | 大点时改用 quad |
| `THREE.Points` 无法各向异性 | 圆形只能圆 | 笔触/椭圆必须 `InstancedMesh` |
| **屏幕空间风格会"游动"** | 转动相机时点画图案跟着屏幕走（shower-door effect） | 要么接受（像"透过纱窗看"），要么改逐点抖动 |
| 深度图精度不足 | 描边会闪烁 | 先平滑深度（双边滤波）再求梯度 |
| 半透明排序 | 点云没有前后顺序，`depthWrite:false` 会出伪影 | 要么 alpha test（不透明），要么按深度预排序 |
| 加法混合 + 深度 | 加法混合会"穿透"前面的物体 | 粒子层设 `depthTest:false` 并最后画 |
| GIF 循环跳帧 | `uTime` 不回绕 | 按 `frame % cycle` 算时间 |
| 沸腾线频率 | 太高变噪声，太低没感觉 | 8~12 fps 的"重绘频率"最像手绘 |

---

## 10. 参考

- 综述：[A Survey on Non-Photorealistic Rendering Approaches for Point Clouds](https://www.researchgate.net/publication/380937598_A_Survey_on_Non-photorealistic_Rendering_Approaches_for_Point_Cloud_Visualization)（IEEE TVCG 2025）
- 排线：[Art-directed Hatching of 3D Point Clouds](https://pdfs.semanticscholar.org/5052/b24ab920aa84a47755481c17b9ee61b940c8.pdf)
- 笔触：[Stroke-based splatting](https://link.springer.com/article/10.1007/s00371-017-1420-7)
- 点画：[Interactive Stipple Rendering for Point Clouds](https://dspace.zcu.cz/bitstreams/386666f4-28c6-4532-b560-59da98ecee69/download)
- 线描：[Suggestive Contours](https://gfx.cs.princeton.edu/pubs/DeCarlo_2003_SCF/) · Hertzmann《Line drawings from 3D models》
- 高斯上的风格化（延伸）：[Gaussian Splatting in Style](https://arxiv.org/html/2403.08498v2) · [GAGS](https://www.spiedigitallibrary.org/conference-proceedings-of-spie/14351/143511Z/GAGS-graph-guided-adaptive-Gaussian-splatting-for-scene-stylization/10.1117/12.3119870.full)
- splat 经典：[EWA Splatting](https://www.cs.umd.edu/~zwicker/publications/EWASplatting-TVCG02.pdf)（Zwicker et al.）· Pfister《Surfels》(SIGGRAPH 2000)
