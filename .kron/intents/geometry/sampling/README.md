<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T13:58:01.5007131Z
status: active
<!-- /kron:frontmatter -->
# 采样策略（总）

> > 点数买覆盖率，分辨率买可分辨结构，两者分开调。


## 这个节点（总）

总决策：**信息密度驱动采样**，按图像结构与墨量加权，而不是均匀采样。这条下面四个细节分别回答：权重怎么给、点数预算、分辨率预算、工作分辨率与点数的解耦。

## 子意图（细节）

| 子意图 | 它负责的细节 |
|---|---|
| `geometry/sampling/structure-aware` | 按图像结构/墨量加权选点 |
| `geometry/sampling/point-count-budget` | 点数预算：不吃显存，吃体积与传输 |
| `geometry/sampling/resolution-budget` | 分辨率预算是双闸门 |
| `geometry/sampling/workres` | 工作分辨率与 max_points 解耦 |

> 子意图之间是**并列的细节**，不是执行顺序。执行/推导顺序用 frontmatter 的 `depends_on` 表达。
