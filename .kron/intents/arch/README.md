<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-01T08:48:28.1023639Z
status: active
<!-- /kron:frontmatter -->

# arch：三段式架构

> Python 只做推理，Go 做几何与服务，渲染只在浏览器。两个二进制协议串起来。

## 模块边界

本模块只定"谁做什么、边界在哪、协议长什么样"。具体算法实现在 `geometry`，具体画法在 `render`。

## 相关

- 协议完整规范：`docs/selection.md` §4
- Go 侧职责清单：`docs/go-scope.md`

## 这个节点（总）

这一层的总决策是**职责沿进程边界切**：任何"重"的能力都放在 Python（模型），任何"确定且要测"的计算放在 Go（几何、编码），任何"帧率敏感"的东西放浏览器。切完之后三段之间只剩两条契约：image/ILC1 字节流 和 stdio 帧协议。

## 子意图（细节）

| 子意图 | 它负责的细节 |
|---|---|
| `arch/three-way-split` | 三段式具体怎么切、边界在哪（含帧协议与 health 两个细节） |

> 子意图之间是**并列的细节**，不是执行顺序。执行/推导顺序用 frontmatter 的 `depends_on` 表达。
