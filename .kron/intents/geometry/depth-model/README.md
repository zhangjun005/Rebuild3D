<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T13:58:01.3392363Z
status: active
<!-- /kron:frontmatter -->
# 深度模型选型（总）

> > 选 DA3MONO-LARGE，并接受它只有相对深度这个事实。


## 这个节点（总）

总决策：在"能跑在 8GB 上"的前提下要最新的单目模型。选型的直接后果不是"深度更准"，而是**下游必须按相对深度设计**——这一条串到了 projection 子域。

## 子意图（细节）

| 子意图 | 它负责的细节 |
|---|---|
| `g` | e |
| `D` | A |

> 子意图之间是**并列的细节**，不是执行顺序。执行/推导顺序用 frontmatter 的 `depends_on` 表达。
