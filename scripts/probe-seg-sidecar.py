#!/usr/bin/env python
"""SEGMENT 命令的协议冒烟测试（也是这个新命令的可执行规范）。

拉起 sidecar → 发 PING → 发 SEGMENT（带一张插画）→ 校验响应结构。
只测帧协议与 mask 尺寸，不评判分割质量（那是 probe-seg.py 的事）。

用法：python scripts/probe-seg-sidecar.py
"""
from __future__ import annotations

import json
import struct
import subprocess
import sys
from pathlib import Path

PY = r"E:\conda\envs_dirs\illust3d\python.exe"
SERVER = Path("sidecar/server.py")
IMG = Path("testdata/illustrations/平涂131905131_p0.png")

CMD_PING, CMD_SEGMENT = 1, 4
U32 = struct.Struct("<I")


def read_exact(stream, n: int) -> bytes:
    buf = bytearray(n)
    got = 0
    while got < n:
        k = stream.readinto(memoryview(buf)[got:])
        if not k:
            raise EOFError(f"期望 {n} 字节，只读到 {got}")
        got += k
    return bytes(buf)


def send(proc, cmd: int, arg: int, body: bytes) -> tuple[int, bytes]:
    payload = bytes([cmd]) + U32.pack(arg) + body
    proc.stdin.write(U32.pack(len(payload)))
    proc.stdin.write(payload)
    proc.stdin.flush()
    (total,) = U32.unpack(read_exact(proc.stdout, 4))
    status = read_exact(proc.stdout, 1)[0]
    return status, read_exact(proc.stdout, total - 1)


def main() -> int:
    img_bytes = IMG.read_bytes()
    print(f"图片: {IMG.name}  {len(img_bytes) / 1e6:.2f} MB")

    proc = subprocess.Popen(
        [PY, str(SERVER), "--lazy", "--res", "504"],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    ok = 0
    fail = 0

    def check(name: str, cond: bool, detail: str = "") -> None:
        nonlocal ok, fail
        if cond:
            ok += 1
            print(f"  ✅ {name}{' — ' + detail if detail else ''}")
        else:
            fail += 1
            print(f"  ❌ {name}{' — ' + detail if detail else ''}")

    try:
        # ① PING
        status, body = send(proc, CMD_PING, 0, b"")
        check("PING status=0", status == 0)
        info = json.loads(body)
        check("PING 返回 JSON 且有 ready 字段", "ready" in info, str(info)[:120])

        # ② SEGMENT
        status, body = send(proc, CMD_SEGMENT, 0, img_bytes)
        if status != 0:
            check("SEGMENT status=0", False, body.decode("utf-8", "replace")[:200])
        else:
            w, h = struct.unpack("<II", body[:8])
            mask = body[8:]
            check("SEGMENT status=0", True)
            check("尺寸与图片一致", (w, h) == (4000, 4000), f"{w}x{h}")
            check("mask 字节数 = W*H", len(mask) == w * h, f"{len(mask)} vs {w*h}")
            vals = set(mask[i] for i in range(0, len(mask), 7919))  # 抽样看取值
            check("mask 三档取值", vals <= {0, 128, 255}, f"抽样值 {sorted(vals)[:6]}")
            char_ratio = sum(1 for i in range(0, len(mask), 7919) if mask[i] == 255)
            total = len(mask[::7919])
            check("角色占比合理(5%~95%)", 0.05 < char_ratio / total < 0.95, f"{char_ratio/total*100:.1f}%")

        # ③ 再 PING 一次，确认管道没失步
        status, body = send(proc, CMD_PING, 0, b"")
        check("SEGMENT 后管道仍正常", status == 0)
    finally:
        try:
            proc.stdin.close()
        except Exception:  # noqa: BLE001
            pass
        try:
            proc.wait(timeout=20)
        except subprocess.TimeoutExpired:
            proc.kill()

    print(f"\n========== 通过 {ok} / 失败 {fail} ==========")
    return 0 if fail == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
