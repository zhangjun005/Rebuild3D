<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:34.8994475Z
status: active
<!-- /kron:frontmatter -->
# data：跨段数据（总）

> > 整条链路只有两份跨进程数据：ILC1 点云与 sidecar 帧。


## 模块边界

这一层的总决策是**格式即契约，必须向后兼容**。ILC1 的布局直接决定前端能不能零拷贝、加属性会不会破坏老文件；mask 这类"看起来只是数组"的东西，其取值语义一旦定错会让下游整片出错。

## 子意图（细节）

| 子意图 | 它负责的细节 |
|---|---|
| `data/ilc1-format` | 8+15n 字节，零拷贝直传 BufferAttribute |
| `data/mask-thresholds` | 未校准概率不能用窄带三档阈值 |

> 子意图之间是**并列的细节**，不是执行顺序。执行/推导顺序用 frontmatter 的 `depends_on` 表达。
