<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-01T08:48:28.0193411Z
status: active
<!-- /kron:frontmatter -->

# product：这个项目是什么

> 一张插画进，一个能转能动、有画风的东西出。目标"好看"，不追求"准确"。

## 模块边界

本模块只回答"做什么、为什么是它、放弃了什么"。技术怎么实现不在本模块。

## 不在本模块内

- 怎么渲染 → `render`
- 怎么切职责 → `arch`
- 怎么装环境 → `env`

## 这个节点（总）

这一层的总决策是**目标从"准确重建"改成"好看的画面"**，所以精度焦虑不成立。围绕它有三块：主线定义（含砍掉的范围）、go/no-go 的历史结论、以及一个尚未启动的备选方向。

## 子意图（细节）

| 子意图 | 它负责的细节 |
|---|---|
| `product/illustration-pointcloud` | 主线定义（总）与它的子决策 |
| `product/depth-go-no-go` | go/no-go 的结论：条件性通过 |
| `product/multi-layer-parallax` | 备选方向：多层 2.5D 视差（未启动） |

> 子意图之间是**并列的细节**，不是执行顺序。执行/推导顺序用 frontmatter 的 `depends_on` 表达。
