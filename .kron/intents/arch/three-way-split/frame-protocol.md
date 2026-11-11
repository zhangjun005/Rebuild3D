<!-- kron:frontmatter -->
created_by: '@zhangjun005'
updated_at: 2026-11-10T12:58:45.4039356Z
status: active
<!-- /kron:frontmatter -->
# Go↔Python 帧协议：成对消费与失步修复

> Go↔Python 只有一条管道，请求/响应必须**成对消费**。

曾经因为"客户端取消后只读了半个响应"，剩余字节被当成下一帧的长度，管道永久失步（日志：响应长度非法 926505755），表现为 HTTP 200 + Content-Length: 0 的静默失败、health 却仍 ready。根因修复在 client.call()：父 ctx 取消后继续把这帧读完（结果丢弃），读不干净才判定失步并重建整条管道；预算层分三档（失步重建 3 次 / 进程崩溃重启 1 次 / 业务错误不重启）。
