<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:34.7124886Z
status: active
<!-- /kron:frontmatter -->
# tooling：验证工具链（总）

> > 所有验证都靠自动截图 + 数值对比，不靠肉眼看窗口。


## 模块边界

总决策：**验证必须可复现**。浏览器里的点云渲染没法靠"看起来对"来判定，所以自建截图 harness（本地 Edge + playwright），并把"诊断脚本的轮询条件该怎么写"沉淀成纪律。

## 子意图（细节）

| 子意图 | 它负责的细节 |
|---|---|
| `tooling/screenshot-harness` | shot.cjs：唯一取证工具 |
| `tooling/diagnostic-discipline` | 轮询条件必须是本次操作引起的变化 |

> 子意图之间是**并列的细节**，不是执行顺序。执行/推导顺序用 frontmatter 的 `depends_on` 表达。
