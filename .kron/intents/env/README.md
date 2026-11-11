<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-01T08:48:28.1856089Z
status: active
<!-- /kron:frontmatter -->

# env：运行环境

> > 单独 conda env `illust3d`，不碰 base；所有缓存与模型落 E 盘。


## 模块边界

本模块只管"东西装在哪、怎么隔离"。装什么模型在 `geometry`，装什么依赖在实现时定。

## 为什么单独成模块

因为这是**全项目最容易造成不可逆损害的一环**：污染 base 会让 DSH 和其他工具一起崩，
缓存落 C 盘会直接塞满系统盘（只剩 3.9GB）。这一环出错不是"重装"能轻易恢复的。

## 这个节点（总）

这一层的总决策是**把资源约束前置**：C 盘只剩几 GB、显存 8GB、base 环境不能碰。三条约束分别对应 conda 隔离、落盘位置、显存-分辨率换算表。违反其中任何一条都会在"装个包"或"提个分辨率"时炸。

## 子意图（细节）

| 子意图 | 它负责的细节 |
|---|---|
| `env/conda-isolation` | 只用 illust3d，绝不碰全局/base |
| `env/disk-budget` | 缓存与模型全部落 E 盘 |
| `env/gpu-vram-budget` | 8GB 显存与 process_res 的换算 |
| `env/model-cache-offline` | 权重走本地快照，断网也能跑 |

> 子意图之间是**并列的细节**，不是执行顺序。执行/推导顺序用 frontmatter 的 `depends_on` 表达。
