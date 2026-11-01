# web — illust3d 渲染层

浏览器里的风格化点云渲染器。**渲染只在浏览器**（[docs/selection.md](../docs/selection.md) §4.3），
风格规格见 [docs/rendering-styles.md](../docs/rendering-styles.md)。

第一阶段实现的是 §7 的第一套预设「**点画印刷**」：
L1 软边圆片 + L2 blue noise 点画 + L2 深度描边 + L3 沸腾 + L4 空气透视 / 单色墨点。
A 阶段加上**循环动图导出**（WebM 录制 / 帧序列 PNG / 无缝自检）。

技术栈：**Vite + TypeScript（strict）+ three.js，无框架**。shader 全部独立成 `.glsl` 文件，用 `?raw` 导入。

---

## 跑

```bash
cd web
npm install
npm run dev        # http://127.0.0.1:5173
npm run build      # tsc --noEmit && vite build → web/dist
npm run preview    # 用 dist 跑一遍（preview 也挂了 testdata 中间件）
npm run gen:noise  # 重新生成 src/assets/blue-noise.png（一次就够，产物已进仓库）
```

点云来源：开发期由 `vite.config.ts` 里的中间件把仓库根的 `testdata/sample.ilcl`
**只读**转发到 `/__testdata/sample.ilcl`（不复制、不修改 `testdata/`）。
生产环境换成 Go 的 `GET /api/cloud/{id}`（同样的 ILC1 字节流，见 `docs/selection.md` §4.2），
`src/ilcl.ts` 里的候选来源列表已经留好了这个回退。

## 相机限位 + 摆动（A2，go/no-go 的后果）

**真实插画的深度只给「剪影 + 平板」，没有主体内部结构**，所以不能 360° 环绕 ——
转到侧面就露馅。于是：

- 环绕改成**来回摆动**（`sin`），不是单向转圈
- 方位角与俯仰角都限制在取景基准的 **±`swayAmplitude`** 内（参数 0~45°，**默认 15°**）
- 俯仰摆幅 = 方位摆幅 × 0.5（只给一点点，避免看到板的上下边缘）
- `swayAmplitude = 0` 时相机锁死在正面（只看平面剪影的用法）
- 手动拖拽会**自动关掉自动摆动**（手和自动运动不打架），且拖拽范围同样受 ±幅度限制
- OrbitControls 的 `min/maxAzimuthAngle`、`min/maxPolarAngle` 都按幅度设置

> 接真实数据时，这一对「取景基准角度」应该来自深度图的正面方向（相机正对 `-Z`），
> 现在用的是自动取景的 3/4 视角（测试点云没有"正面"概念）。

## 半调漫画（B 阶段）

`halftone-manga` 预设 = §2.4 半调网点 + 粗描边 + 黑白。

**网点放在合成 pass，不放在点材质里**：半调是「对连续调图像做网点分色」，
网屏必须是屏幕空间的连续网格；在每个 2~3 px 的点精灵内部做网点会把点切碎，
点与点之间没有连续性，读起来是噪点而不是网点。所以颜色 pass 先把点云画成一张
连续调图像（`stipple = 0`，小点径），合成 pass 再对它做网点化
（`styles/halftone.frag.glsl`）。

```
vec2 g = rot(fragCoord + 抖动偏移, screenAngle)
vec2 cell = fract(g / cellSize) - 0.5
float r = sqrt(1.0 - pow(lum, densityGamma)) * 0.5   // 半径 ∝ sqrt(覆盖率)，印刷公式
dot = 1 - smoothstep(r - 0.07, r + 0.07, length(cell))
颜色 = mix(纸, 墨, dot)，只对「有几何覆盖」的像素做（用深度图判断，背景保持纯纸）
```

- 参数：`半调网点`（强度 0~1，>0 时切到半调合成 pass）、`网点大小 px`（3~14，默认 8）、
  `网屏角度 °`（默认 45，印刷常用 15/45/75）
- **§3 抖动换序**：网点网格按沸腾相位整体平移（< 一个 cell）→ 网点重排的手绘/印刷感
- 半径系数必须是 0.5：用 0.72 会让中间调就糊成实黑（网点彼此粘连），0.5 才是完整调子范围

## 循环动图导出（A 阶段）

面板底部「循环动图导出」（钉在面板底部，不随参数区滚动）：时长 4 / 6 / 10 s、
尺寸 `1080×1080`（默认）/ `1920×1080` / 当前视口，
三个动作：**录制 WebM**、**导出帧序列**、**无缝自检**。
文件名带预设与关键参数：`stipple-print_g1.3_d0.9_p2.49_e1_sw15_1080x1080_6s.webm`
（`g`=明暗曲线 `d`=点画强度 `p`=点大小 `e`=描边强度 `sw`=摆动幅度°）。

### 无缝循环的对齐算式

捕获不走真实时间：`Viewer.beginCapture()` 把一切随时间变化的状态变成**帧索引 i 的纯函数**。

```
输出帧率 30 fps（固定，不跟显示器 144Hz 走）
N 帧 = seconds × 30            （4s→120、6s→180、10s→300）

摆动:   θ(i)         = 2π · swings · (i mod N) / N          swings = 1
        azimuth(i)   = baseAzimuth   + A · sin(θ(i))        A = 摆动幅度（弧度）
        elevation(i) = baseElevation + A·0.5 · sin(θ(i))
沸腾:   framesPerPhase = 30 / boilHz = 3   （按能整除 N 取整）
        phases = N / framesPerPhase
        phase(i) = floor((i mod N) / framesPerPhase) mod phases
```

因为两边都先取模，`i = N` 与 `i = 0` 逐位相同（`sin(0) = sin(2π) = 0`）；
而 `i` 从 `N-1` 走到 `0` 的一步是 `sin` 曲线的连续延伸（摆动连续）和一个相位切换的边界
（沸腾连续）。所以「N 帧、首尾相接」本身就是无缝循环，**不需要裁帧**。
相机在捕获期间**完全由上面的公式覆写**（`controls.enabled = false`），没有第三个时间源。

> 注意：实时视图里的沸腾是 **0.8 s / 8 相** 的循环，6 s 不是它的整数倍 —— 所以录制时
> **不能**复用那个时钟，而是按 `N` 重新推导相位（重绘频率仍是 10 Hz，`3` 帧一相 ×
> `N/3` 相）。这就是「T 必须是 0.8 s 整数倍」那个限制的解除办法：把相位按循环长度归一化。
> 约束只剩一条：`N % 3 == 0`（4/6/10 s 都满足）。

其它随时间变的量：`fogAmount` / 描边参数都是静态的；相机在捕获期间**完全由上面的公式覆写**
（`controls.enabled = false`），`autoRotate` 不参与；所以没有第三个时间源。

### 证据（实测，headless chromium / RTX 4060）

| 项 | 结果 |
|---|---|
| 无缝自检（index 0 vs index N 的 PNG SHA-256） | **相同**（`bf26ca5af35f…`） |
| 录出的 WebM | VP9 · 1080×1080 · 120 帧 · 7.40 MB（4s） |
| 回读时长 | 3.978 s vs 理论 `(N-1)/30 = 3.967 s` → 正好 N 帧，**无首尾重复帧** |
| 录制期间渲染循环 | 140~144 fps（rAF 未卡） |
| 帧序列 | 120 帧 PNG / 252.3 MB（1080² 每帧 ≈ 2.1 MB） |

产物与截图在 `web/.shots/capture/`（`.webm` 可直接播）、`web/.shots/16~19-export-*.png`。

### 帧序列接口（给 Go 侧 `image/gif` 编码）

```js
// N 帧 PNG Blob 数组（首尾相接即无缝循环）
const frames = await window.illust3d.captureFrames(180);                       // 1080×1080
const small  = await window.illust3d.captureFrames(180, { size: { width: 540, height: 540 } });
await window.illust3d.recordWebM(6, 'square');   // → { blob, plan, mimeType }
await window.illust3d.verifySeam(6, 'square');   // → { identical, hashFirst, hashWrap, plan }
```

GIF 编码不在前端做（不引第三方库）。注意 PNG 很大（1080² 每帧约 2.1 MB），
Go 侧建议按 **540~720 px** 取帧、或分块接收，别一次性传 180 × 2 MB。

## 操作

拖拽旋转 · 滚轮缩放 · `H` 收起面板。
面板：预设（点画印刷 / 线描 / 原色调试）+ 点大小、点数密度、点画强度、明暗曲线、
描边强度/阈值/宽度、空气透视、单色墨点、纸颗粒、暗角、沸腾开关、自动慢速旋转、背景/纸色。

调试入口（控制台 / 自动化）：

```js
window.illust3d.stats()       // { fps, frame, points, drawCalls, boilPhase, width, height,
                              //   cameraDistance, coreRadius, pointSize }
window.illust3d.viewer        // Viewer 实例
window.illust3d.syncPanel?.() // 直接改 viewer.params 后刷新面板显示
window.illust3d.loadILC1?(buf, 'label') // 换一份 ILC1 点云（真实深度点云/测试用），返回点数
window.illust3d.captureFrames?(180)     // 帧序列 PNG Blob[]（见上面「循环动图导出」）
```

## 自适应（真实点云不等于测试点云）

`Viewer.setCloud()` 会按新点云重算所有和尺度相关的量，**不写死任何场景常量**：

| 量 | 怎么定 |
|---|---|
| 相机目标点 | 包围盒中心 |
| 相机距离 | `核心半径 / sin(fov/2) × 0.9`；核心半径 = 到中心距离的 **75 分位**（不被大片地面/背景点的长尾拉飞） |
| 相机 near / far | 由取景距离推 |
| `uPointSize` 的参考视距 `refDepth` | `取景距离 × 0.91` → 换尺度后点的屏幕大小不变 |
| 空气透视 / 描边归一化深度区间 | `refDepth` 的 0.57 / 1.82 倍 |
| 点大小 | 估算屏幕点间距：`间距 ≈ 对角线 / sqrt(点数)` → `点大小 ≈ 间距 × 0.5`（clamp 1.2~8 px） |
| 深度 pass 的点放大 | 固定 4×（点大小已随间距自适应，所以覆盖率不塌） |

实测（`web/.shots/12~15-adaptive-*.png`）：

| 输入 | 点数 | 核心半径 | 相机距离 | 自适应点大小 |
|---|---|---|---|---|
| 原始 | 118,401 | 1.98 | 4.97 | 2.49 |
| 均匀抽稀 | 59,200 | 1.98 | 4.97 | 3.53（×1.41） |
| 再抽稀 | 19,733 | 1.98 | 4.97 | 6.09（×2.44） |
| 尺度 ×5 + 平移 | 118,401 | 9.90 | 24.87（×5） | 2.49（不变） |

## 目录

```
web/
  index.html                 # canvas + 面板宿主（样式也在这里，够用就不引 CSS 框架）
  vite.config.ts             # testdata 只读中间件（dev + preview）
  scripts/gen-blue-noise.mjs # void-and-cluster 生成 256² blue noise PNG（无依赖）
  src/
    main.ts                  # 启动：纹理 → Viewer → Panel → 点云 → 跑
    viewer.ts                # 渲染核心：4 个 pass、uniform 管理、沸腾时钟、FPS
    ilcl.ts                  # ILC1 解析（零拷贝视图）+ 来源回退
    params.ts                # 参数类型 + 三套预设
    capture.ts               # A 阶段：WebM 录制 / 帧序列 / 无缝自检 / 文件名
    panel.ts                 # 纯 DOM 控制面板
    assets/blue-noise.png    # 由 scripts/gen-blue-noise.mjs 生成
    styles/                  # 全部 GLSL
      points.vert.glsl        #   点大小 + 空气透视 + 逐点点画/沸腾
      stipple-print.frag.glsl #   L1 软边圆片 + 空气透视 + 单色墨点
      depth.vert.glsl         #   深度 pass：归一化线性深度 + 圆盘
      depth.frag.glsl         #   深度 pass 写出（必须圆形 discard，见文件内注释）
      smooth-depth.frag.glsl  #   填洞 + 5×5 双边平滑（描边前的深度清理）
      quad.vert.glsl          #   全屏四边形
      post-edge.frag.glsl     #   合成（点画印刷）：sobel 深度描边 + 暗角 + 纸颗粒
      halftone.frag.glsl      #   合成（半调漫画）：屏幕空间网点 + 粗描边 + 暗角/颗粒
```

## 渲染管线（4 个 pass）

```
1. 深度 pass   点云 → HalfFloat RT，写归一化线性深度（背景 = 1.0 = 最远）
               点放大 4× 封住点间隙；**圆形 discard**，否则方形 tile 会被描边描成方框
2. 平滑 pass   5×5 双边（以中心为参考 + 条件性填洞）→ 干净、保留台阶的深度图
3. 颜色 pass   点云 → 纸色底 RT（软边圆片 + 逐点点画 + 空气透视 + 墨点）
4. 合成 pass   色彩 + sobel 深度描边（+ 极小 Laplacian 补刀）+ 暗角 + 纸颗粒 → 屏幕
```

为什么深度单独一遍、不用主 pass 的 depth buffer：

1. 需要用 HalfFloat 存**归一化线性深度**，8 位会带出条带 → 描边闪（§9）；
2. 点云深度有洞，可以在深度 pass 里把点放大封洞，而不影响画面；
3. **描边不随「点数密度」塌陷** —— 密度调低时线仍然连续，这是美术控制。

## 几个实现决定（和规格不完全一致的地方）

- **点画用「逐点哈希」而不是 §8 的屏幕空间 blue noise**。实测：屏幕空间噪声采样在
  **规则点阵**上会拍频出摩尔纹（测试球面是 Fibonacci 点阵，间距 ~5 px，blue noise cell 2.5 px，
  拍出整片斜向条纹，球面被纹上假纹理）。这是采样定理问题，调参数消不掉。
  §2.1 列的路线二「逐点抖动」正是为此：每点用自己的确定性哈希做保留决策 → 与屏幕无关、
  转动不游走、无摩尔纹、3D 稳定。blue noise 纹理仍然保留并使用：L4 纸颗粒（合成 pass），
  后续半调 / 抖动换序（§3）也用同一张图；生成脚本照 §8 要求自己写。
- **密度 = 1 − 亮度**（暗处点密，§2.1 原文）。注意 §8 的 10 行起手式是 `n > lum` 才 discard
  （亮处留），那是屏幕空间抖动「提亮」的写法；做成点云点画时方向要反过来，
  否则暗部反而空掉。
- **单色墨点 = 统一墨色**，明暗只由疏密表达。若点色也随亮度变化（duotone），
  密度与深浅会双重计数，中间调糊成灰。
- **点数密度用逐点抽稀，不用 `setDrawRange`**。ILC1 的点按「地面 → 球 → 立方体」排列，
  按顺序截断会让密度一降就只剩地面。
- **沸腾相位由「按 60 fps 折算、并对循环长度取模」的逻辑帧计驱动**：换 144Hz 屏也是 10 Hz 重绘，
  而且时间可回绕（§9「GIF 循环跳帧」）。沸腾 = 整个点画的去留按相位置掷一次（手绘重画感）+ 描边微抖。
- **颜色空间**：关闭 `ColorManagement`、`outputColorSpace = LinearSRGBColorSpace`，
  全链路在显示空间计算。着色器是风格化不是物理渲染，避免两次 8 位量化与意外的 gamma。

## 已知问题 / 下一步

- 描边在点很稀的轮廓上会有 ~1 个点距的锯齿（手绘感，也可当 feature）；想要更直的线，
  下一步可以做深度图的形态学闭运算或多尺度梯度。
- 还差四套预设（铜版画 / 油画 / 像素立体书 / 梦境星空）；版画 §2.6 与像素 §2.7 也是「加一个合成片元 shader」的量级。
- 半调现在只有单网屏（一个角度）。真正的彩印是 CMYK 四色四角度（15/45/75/0），需要四个网屏叠加；
  漫画单色用不到，但如果以后要做彩印半调，得在 halftone.frag.glsl 里加四层。
- `gl_PointSize` 上限驱动相关（§9），已 clamp 到 255，大点时应该改 quad 基元。
- 逐点抖动在极低密度下会有轻微团簇（点云本身是规则点阵时更明显），真实深度点云不会。
  注意：把测试点云做「隔 N 取一」抽稀会保留 Fibonacci 子点阵，屏幕上会看到梳状条纹
  （`web/.shots/14-adaptive-20k.png`）——这是合成数据的规则性，不是渲染器问题。
- 掠射面（几乎侧视的窄面）墨点偏密：密度只看亮度，没算法线·视线的掠射因子。已知，待定。
- 点云里真实的空洞（置信度过滤造成）目前靠深度 pass 的 4× 放大 + 5×5 填洞兜住；
  空洞很大时描边会在洞边画出假轮廓，后续可以按「邻域覆盖度」抑制。
