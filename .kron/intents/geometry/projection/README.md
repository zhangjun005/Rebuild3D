<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T13:58:01.4227365Z
status: active
<!-- /kron:frontmatter -->
# 反投影方式（总）

> > 用正交反投影，不用透视——因为模型没有相机。


## 这个节点（总）

总决策：**没有内参就不该假装有相机**。透视公式 x=(u-cx)·d/f 里 f 是猜的、d 无量纲，等于两个假设相乘；正交 x=(u-cx)·s 只有一个自由参数，正好匹配"相对深度"这个事实。z 的尺度则降级为艺术参数。

## 子意图（细节）

| 子意图 | 它负责的细节 |
|---|---|
| `geometry/projection/relative-depth` | 前提事实：相对深度、无内参/外参/conf |
| `geometry/projection/orthographic` | 主决策：正交反投影（可混合透视） |
| `geometry/projection/depth-scale` | z 轴压缩 = 浮雕厚度 |
| `geometry/projection/z-sign` | z = -d 的符号约定修正 |
| `geometry/projection/perspective` | 已废弃：透视反投影（superseded） |

> 子意图之间是**并列的细节**，不是执行顺序。执行/推导顺序用 frontmatter 的 `depends_on` 表达。
