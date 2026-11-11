# Rebuild3D · 决策文档

<!-- @kron:intent product/illustration-pointcloud -->
<!-- @kron:intent env/disk-budget -->

这个目录只放**决策与规范**，不放代码说明。目的：半年后回来看，能知道"当初为什么这么定"。

---

## 当前主线

> **插画 → 点云 → 风格化渲染**
> 一张插画进去，出来一个能转、能动、有画风的东西。
> 目标从"准确的空间重建"改为**"好看的画面"**——所以所有精度焦虑在这条线上不存在。

```
一张插画
   ├─► 【图像模式】原图 → 全屏 quad → 风格化（不碰深度，信息量 100%）← 主答案
   └─► 【点云模式】深度估计 → 反投影成点云 → 风格化（有视差，信息量 1.24%）← 配角
            └─► 可交互网页 + 循环 WebM/GIF
```

**最新阶段成果见 [milestone-1.md](milestone-1.md)。**

---

## 活跃文档

| 文档 | 内容 | 该不该改 |
|---|---|---|
| **[milestone-1.md](milestone-1.md)** | **★ 阶段小结**：交付了什么、推翻了哪些判断、12 个坑、方法学、未完成清单 | 每阶段末追加 |
| **[scope.md](scope.md)** | 主线定义、技术方案、分阶段、验证顺序 | 方向变了就改 |
| **[selection.md](selection.md)** | **★ 选型与规范**：conda 隔离、模型选型、仓库结构、接口协议、相机约定、资源预算 | **施工依据** |
| **[rendering-styles.md](rendering-styles.md)** | **★ 风格渲染完整清单**：三层结构、7 种风格、6 套预设、参数表、坑 | 新增风格就加 |
| **[rendering-methods.md](rendering-methods.md)** | **★ 点云渲染范式清单 + 完成情况表**：55 项候选方法、状态跟踪、验收标准、实验登记 | 实现一项就改状态 |
| **[go-scope.md](go-scope.md)** | Go 的职责边界与技术选型 | 职责变了就改 |

---

## 归档（历史思考，不可当施工图）

| 文档 | 内容 |
|---|---|
| [archive/legacy-inventory.md](archive/legacy-inventory.md) | 旧工程（已丢失）的遗产盘点 —— `code/` 目录是什么、为什么跑不起来 |
| [archive/business-reassessment.md](archive/business-reassessment.md) | 业务重估 + 点云编辑生态调研 + Go/Python 分工的最初论证 |
| [archive/01-product-v1.md](archive/01-product-v1.md) | **原主线**：空间采集/户型工具的产品定义（已降级为备选） |
| [archive/02-direction-expansion.md](archive/02-direction-expansion.md) | 方向扩充：五方向评估（A 户型 / B 转台 / C 参考采集 / D 文保 / E 纯深度） |
| [archive/03-pointcloud-and-rendering.md](archive/03-pointcloud-and-rendering.md) | 点云质量归因、A 方向四阶段拆解、单图 LDI 路线 |

> **A 方向（户型/尺寸）没有废弃。** 它的 go/no-go 关卡是"俯视投影能不能看出户型"，只要 1 天。
> 如果这条插画线做腻了，随时可以切回去——深度引擎是共用的。

---

## 环境与硬件事实

| 项 | 值 |
|---|---|
| GPU | RTX 4060 Laptop **8 GB** |
| RAM | 15.2 GB |
| CPU | Ryzen 9 7845HX |
| **C 盘** | **仅剩 3.9 GB** ⚠️ 一切缓存必须落 E 盘 |
| E 盘 | 110 GB 可用 |
| conda | 25.5.1，`envs_dirs=E:\conda\envs_dirs`，`pkgs_dirs=E:\conda\pkg_dirs` |
| 专用环境 | **`illust3d`**（Python 3.10）—— **绝不用 base（3.13.5）** |
| Go | 1.27.1，`GOPROXY=goproxy.cn` |
| Node | 22.23.3 |

---

## 决策记录方式

暂用 Markdown（本目录）。等方向稳定后，把"为什么"迁到 kron 的 `.kron/intents/`，这里只留"是什么"。

**铁律三条**（见 [selection.md](selection.md) §0）：
1. 只在 conda env 里装东西，绝不碰全局
2. 所有缓存与模型都落 E 盘
3. 不引用 `code/` 旧工程作为依赖
