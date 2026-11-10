<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:45.4830729Z
<!-- /kron:frontmatter -->
# health 必须如实反映可用性

> One-line summary of this intent's decision.

## Why

Record the reason for this decision.

## Trade-offs

What was chosen and what was given up.

<!-- 边界假设写在 frontmatter 的 assumptions 字段里，不在正文重复 -->

## Why

出过两次"服务看起来正常、其实坏了"：管道失步后 health 仍报 ready=true；崩溃重启预算耗尽后也还能返回 200。规则：任何让管道不可用的错误都必须把 ready 置 false 并写明原因，宁可让前端报错，也不能让用户以为还能用。Health 是运维接口，不是装饰。
