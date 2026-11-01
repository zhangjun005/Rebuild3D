#!/usr/bin/env python
"""sidecar 的独立冒烟测试：不经过 Go，直接按帧协议跟 server.py 对话。

用途：
  1. 改 sidecar 后先在这里验证，不用等 Go 侧
  2. 作为帧协议的**可执行规范**（Go 侧 internal/infer 应与它行为一致）

用法（用 illust3d 环境的 python）：
    python scripts/sidecar-smoke.py                       # 用 testdata/illustrations 里第一张
    python scripts/sidecar-smoke.py path/to/img.png
    python scripts/sidecar-smoke.py --no-infer            # 只测 PING/UNLOAD（不加载模型，很快）

退出码：0 = 全通过
"""
from __future__ import annotations

import argparse
import json
import struct
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SERVER = ROOT / "sidecar" / "server.py"
OUT = ROOT / "out" / "smoke"

U32 = struct.Struct("<I")

CMD_PING, CMD_INFER, CMD_UNLOAD = 1, 2, 3


class Sidecar:
    def __init__(self, args: list[str]):
        self.proc = subprocess.Popen(
            [sys.executable, str(SERVER), *args],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=None,  # 直通到终端，方便看 sidecar 日志
            cwd=str(ROOT),
        )
        assert self.proc.stdin and self.proc.stdout

    def call(self, cmd: int, arg: int = 0, body: bytes = b"") -> tuple[int, bytes]:
        frame = bytes([cmd]) + struct.pack("<I", arg) + body
        self.proc.stdin.write(U32.pack(len(frame)))
        self.proc.stdin.write(frame)
        self.proc.stdin.flush()

        head = self._read_exact(4)
        (total_len,) = U32.unpack(head)
        payload = self._read_exact(total_len)
        return payload[0], payload[1:]

    def _read_exact(self, n: int) -> bytes:
        buf = b""
        while len(buf) < n:
            chunk = self.proc.stdout.read(n - len(buf))
            if not chunk:
                raise EOFError(f"sidecar 提前关闭（期望 {n} 字节，收到 {len(buf)}）")
            buf += chunk
        return buf

    def close(self) -> int:
        if self.proc.stdin:
            self.proc.stdin.close()
        return self.proc.wait(timeout=30)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("image", nargs="?", help="测试用图片；缺省取 testdata/illustrations 里第一张")
    ap.add_argument("--no-infer", action="store_true", help="跳过 INFER（不加载模型）")
    ap.add_argument("--res", type=int, default=504)
    ap.add_argument("--out", default=str(OUT))
    args = ap.parse_args()

    ok = True

    def check(name: str, cond: bool, detail: str = "") -> None:
        nonlocal ok
        mark = "PASS" if cond else "FAIL"
        if not cond:
            ok = False
        print(f"  [{mark}] {name}" + (f"  {detail}" if detail else ""))

    print("启动 sidecar …")
    t0 = time.time()
    sc = Sidecar(["--res", str(args.res), "--allow-network"])
    try:
        # ── PING ──
        status, body = sc.call(CMD_PING)
        st = json.loads(body) if status == 0 else {}
        print(f"\nPING ({time.time()-t0:.1f}s): {json.dumps(st, ensure_ascii=False)}")
        check("PING status=0", status == 0)
        check("PING 返回 ready", st.get("ready") is True, f"ready={st.get('ready')}")
        check("PING 有 vram_used_mb", "vram_used_mb" in st)

        # ── INFER ──
        if not args.no_infer:
            img = Path(args.image) if args.image else None
            if img is None:
                cand = sorted((ROOT / "testdata" / "illustrations").glob("*"))
                cand = [p for p in cand if p.suffix.lower() in {".png", ".jpg", ".jpeg", ".webp"}]
                if not cand:
                    print("\n找不到测试图片，跳过 INFER")
                    img = None
                else:
                    img = cand[0]

            if img is not None:
                print(f"\nINFER: {img.name} ({img.stat().st_size/1024:.0f} KB)")
                t = time.time()
                status, body = sc.call(CMD_INFER, 0, img.read_bytes())
                dt = time.time() - t
                check("INFER status=0", status == 0, "" if status == 0 else body[:200].decode("utf-8", "replace"))
                if status == 0:
                    w, h, flags = struct.unpack("<III", body[:12])
                    depth_bytes = body[12:]
                    expect = w * h * 4
                    check("尺寸合理", 64 <= w <= 4096 and 64 <= h <= 4096, f"{w}x{h}")
                    check("flags bit0=0（DA3MONO 无 conf）", flags & 1 == 0, f"flags={flags}")
                    check(
                        "depth 字节数 = W*H*4",
                        len(depth_bytes) == expect,
                        f"{len(depth_bytes)} vs {expect}",
                    )
                    try:
                        import numpy as np

                        d = np.frombuffer(depth_bytes, dtype=np.float32)
                        check("无 NaN", bool(np.isfinite(d).all()))
                        check(
                            "值域合理",
                            bool(d.min() > 0 and d.max() < 1e4),
                            f"min={d.min():.3f} max={d.max():.3f} median={np.median(d):.3f}",
                        )
                        outdir = Path(args.out)
                        outdir.mkdir(parents=True, exist_ok=True)
                        (outdir / f"{img.stem}.depth.f32").write_bytes(depth_bytes)
                        meta = {
                            "image": str(img),
                            "width": w,
                            "height": h,
                            "flags": flags,
                            "seconds": round(dt, 3),
                            "min": float(d.min()),
                            "max": float(d.max()),
                            "median": float(np.median(d)),
                        }
                        (outdir / f"{img.stem}.meta.json").write_text(
                            json.dumps(meta, ensure_ascii=False, indent=2), encoding="utf-8"
                        )
                        print(f"  → 已写 {outdir / (img.stem + '.depth.f32')}")
                    except ImportError:
                        print("  (无 numpy，跳过数值校验)")

                status, body = sc.call(CMD_PING)
                st2 = json.loads(body)
                print(f"\nINFER 后 PING: {json.dumps(st2, ensure_ascii=False)}")
                check("last_infer_seconds 已记录", st2.get("last_infer_seconds") is not None)

        # ── UNLOAD ──
        print("\nUNLOAD")
        status, body = sc.call(CMD_UNLOAD)
        check("UNLOAD status=0", status == 0)
        status, body = sc.call(CMD_PING)
        st3 = json.loads(body)
        check("卸载后 ready=false", st3.get("ready") is False, f"ready={st3.get('ready')}")
        # 注意：卸载后不会归零。PyTorch 的 CUDA 上下文和分配器会留几 MiB 残留（实测 8.1 MiB），
        # 这不是泄漏；真正要确认的是「从 GB 级降回接近 0」。
        check(
            "卸载后显存基本释放（< 64 MiB）",
            st3.get("vram_used_mb", 999) < 64,
            f"vram={st3.get('vram_used_mb')} MiB（峰值 {st3.get('vram_peak_mb')}）",
        )

        # ── 未知 cmd ──
        status, body = sc.call(99)
        check("未知 cmd 返回 status=1", status == 1, body[:80].decode("utf-8", "replace"))

    finally:
        rc = sc.close()
        print(f"\nsidecar 退出码 {rc}")

    print("\n" + ("全部通过" if ok else "有失败项"))
    return 0 if ok else 1


if __name__ == "__main__":
    raise SystemExit(main())
