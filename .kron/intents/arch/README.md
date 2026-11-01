<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-01T08:48:28.1023639Z
status: active
<!-- /kron:frontmatter -->
# arch：三段式架构

> Python 只做推理，Go 做几何与服务，渲染只在浏览器。两个二进制协议串起来。

## 模块边界

本模块只定"谁做什么、边界在哪、协议长什么样"。具体算法实现在 `geometry`，具体画法在 `render`。

## 子意图

- `arch/three-way-split` —— 职责切分与协议

## 相关

- 协议完整规范：`docs/selection.md` §4
- Go 侧职责清单：`docs/go-scope.md`
