<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-01T08:47:59.6723596Z
status: active
depends_on:
    - product/illustration-pointcloud
<!-- /kron:frontmatter -->

# 风格化拆成三层

> L1 基元（一个点画成什么）/ L2 风格（画面是什么画风）/ L3 时间（动起来的质感），三层正交组合。

## 为什么（Why）

把"风格"当成一个整体来调，就没法迭代——每调一次都像重做。拆成三层之后：

- 加一种新风格 = 加一个 shader 文件，不动其他层
- 六套预设只是参数组合（存 JSON），不需要改代码
- 可以交叉组合出没人做过的效果（点画 + 沸腾线、排线 + 版画）

还有一层 L4「后期通用增强」（空气透视、辉光、景深、色调映射），和风格正交，
叠在任何组合上都能提升观感。其中**空气透视（按深度混背景色）是单点收益最高的一招**。

## 如何（How）

完整清单（7 种风格、5 种基元、参数表、6 套预设、实现坑）见 `docs/rendering-styles.md`。
每个 shader 独立成 `.glsl` 文件，用 `?raw` 导入，不塞进 TS 字符串。

## 权衡（Tradeoffs）

- 层之间会互相影响：半调和点画叠加容易糊成一团。需要限制有效组合，不能真的任意交叉。
- 屏幕空间风格在转动相机时会"游动"（shower-door effect）。要么接受（像透过纱窗看），
  要么改成逐点抖动（3D 稳定但成本高）。

## 备选（Alternatives）

- 只做一种风格、做到极致（例如只做点画）：更聚焦，但放弃了"预设可组合"的可玩性

---

## 子意图（细节）

| 子意图 | 它负责的细节 |
|---|---|
| `render/style-layering/primitive-layer` | L1 基元：决定渲染上限 |
| `render/style-layering/ink-mapping` | L3 墨量跟随结构，不跟随绝对亮度 |
| `render/style-layering/density-as-style` | 点密度是风格参数，不是质量指标 |
| `render/style-layering/composite` | 合成层：半调/排线/描边（总） |

> 子意图之间是**并列的细节**，不是执行顺序。执行/推导顺序用 frontmatter 的 `depends_on` 表达。
