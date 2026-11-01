<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-01T08:48:28.1856089Z
status: active
<!-- /kron:frontmatter -->
# env：运行环境

> 单独 conda env `illust3d`，不碰 base；所有缓存与模型落 E 盘。

## 模块边界

本模块只管"东西装在哪、怎么隔离"。装什么模型在 `geometry`，装什么依赖在实现时定。

## 为什么单独成模块

因为这是**全项目最容易造成不可逆损害的一环**：污染 base 会让 DSH 和其他工具一起崩，
缓存落 C 盘会直接塞满系统盘（只剩 3.9GB）。这一环出错不是"重装"能轻易恢复的。

## 子意图

- `env/conda-isolation` —— 隔离策略与三重保障
