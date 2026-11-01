#!/usr/bin/env python
"""illust3d 的 Python 推理 sidecar —— 常驻子进程，走 stdio 帧协议。

**它不是 HTTP 服务，不监听任何端口。** 由 Go 用 os/exec 拉起，通过 stdin/stdout 通信。
理由见 docs/selection.md §4.1（端口/孤儿进程/显存泄漏的风险都省掉了）。

帧格式（全小端）：

  请求  Go → Python:  total_len(u32) + cmd(u8) + arg(u32) + body
  响应  Python → Go:  total_len(u32) + status(u8) + body

  total_len 是「其后字节数」：请求 = 5 + len(body)，响应 = 1 + len(body)

  cmd  1=PING    body 为空                 → 响应 body = UTF-8 JSON 状态
       2=INFER   body = 原始图片字节        → 响应 body = W,H,flags(u32×3) + depth f32[W*H]
       3=UNLOAD  body 为空                 → 响应 body 为空
  status 0=ok，1=错误（此时 body = UTF-8 错误信息）

**日志一律写 stderr。** stdout 是帧协议专线，任何 print 到 stdout 都会破坏协议。
"""
from __future__ import annotations

import argparse
import json
import struct
import sys

from depth_engine import DEFAULT_MODEL, DEFAULT_RES, DepthEngine

CMD_PING = 1
CMD_INFER = 2
CMD_UNLOAD = 3

STATUS_OK = 0
STATUS_ERR = 1

U32 = struct.Struct("<I")


def log(msg: str) -> None:
    print(f"[sidecar] {msg}", file=sys.stderr, flush=True)


def read_exact(stream, n: int) -> bytes:
    """从流里精确读 n 字节。读到 EOF 返回 b''（表示对端关闭）。"""
    buf = bytearray(n)
    view = memoryview(buf)
    got = 0
    while got < n:
        k = stream.readinto(view[got:])
        if not k:
            if got == 0:
                return b""
            raise EOFError(f"帧不完整：期望 {n} 字节，只读到 {got}")
        got += k
    return bytes(buf)


def write_frame(out, status: int, body: bytes) -> None:
    out.write(U32.pack(1 + len(body)))
    out.write(bytes([status]))
    if body:
        out.write(body)
    out.flush()


def handle_ping(engine: DepthEngine) -> bytes:
    return json.dumps(engine.status(), ensure_ascii=False).encode("utf-8")


def handle_infer(engine: DepthEngine, body: bytes) -> bytes:
    if not body:
        raise ValueError("INFER 的 body 为空（应为原始图片字节）")
    depth_bytes, w, h, flags = engine.infer(body)
    return struct.pack("<III", w, h, flags) + depth_bytes


def main() -> int:
    ap = argparse.ArgumentParser(description="illust3d Python 推理 sidecar（stdio 帧协议）")
    ap.add_argument("--model", default=DEFAULT_MODEL)
    ap.add_argument("--res", type=int, default=DEFAULT_RES, help="process_res，默认 504")
    ap.add_argument("--device", default="cuda")
    ap.add_argument(
        "--lazy",
        action="store_true",
        help="不在启动时加载模型，等第一个 INFER 再加载（默认启动即加载，便于 Go 侧预热）",
    )
    ap.add_argument(
        "--allow-network",
        action="store_true",
        help="允许从 HF 拉权重（默认只认本地缓存快照）",
    )
    args = ap.parse_args()

    # stdout/stdin 用二进制，禁用缓冲（帧必须立刻到达对端）
    stdin = sys.stdin.buffer
    stdout = sys.stdout.buffer

    log(f"启动 model={args.model} res={args.res} device={args.device} lazy={args.lazy}")
    engine = DepthEngine(
        model=args.model,
        device=args.device,
        process_res=args.res,
        local_files_only=not args.allow_network,
    )

    if not args.lazy:
        try:
            engine.load()
        except Exception as e:  # noqa: BLE001
            log(f"模型加载失败：{e}")
            # 不直接退出：让 Go 侧通过 PING 看到 ready=false，自行决定重试还是报错
            log("将以未就绪状态继续服务（PING ready=false，INFER 会再试一次加载）")

    log("就绪，等待帧（stdin）")

    while True:
        try:
            head = read_exact(stdin, 4)
        except EOFError as e:
            log(f"帧头不完整：{e}")
            return 2
        if not head:
            log("stdin EOF，对端已关闭，退出")
            return 0

        (total_len,) = U32.unpack(head)
        if total_len < 5:
            log(f"非法帧：total_len={total_len} < 5")
            write_frame(stdout, STATUS_ERR, f"非法帧长度 {total_len}".encode())
            continue

        try:
            payload = read_exact(stdin, total_len)
        except EOFError as e:
            log(f"帧体不完整：{e}")
            return 2
        if not payload:
            log("读帧体时遇到 EOF，退出")
            return 0

        cmd = payload[0]
        (arg,) = struct.unpack("<I", payload[1:5])
        body = payload[5:]

        try:
            if cmd == CMD_PING:
                resp = handle_ping(engine)
            elif cmd == CMD_INFER:
                if arg and arg != engine.process_res:
                    # 允许按请求临时改分辨率（艺术参数，见 selection.md §4.4）
                    log(f"本次请求改 process_res {engine.process_res} → {arg}")
                    engine.process_res = arg
                resp = handle_infer(engine, body)
            elif cmd == CMD_UNLOAD:
                engine.unload()
                resp = b""
            else:
                raise ValueError(f"未知 cmd={cmd}")
            write_frame(stdout, STATUS_OK, resp)
            log(f"cmd={cmd} ok ({len(resp)} B)")
        except Exception as e:  # noqa: BLE001
            msg = f"{type(e).__name__}: {e}"
            log(f"cmd={cmd} 失败：{msg}")
            write_frame(stdout, STATUS_ERR, msg.encode("utf-8", "replace"))


if __name__ == "__main__":
    raise SystemExit(main())
