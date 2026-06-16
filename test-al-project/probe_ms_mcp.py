#!/usr/bin/env python3
"""Probe Microsoft's official `al launchmcpserver` and list its tools."""
import json
import subprocess
import sys
import os
import io
import threading
import queue
import time
from pathlib import Path

if sys.platform == 'win32':
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')
    sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8', errors='replace')

AL_BIN = os.path.expanduser(r"~\.dotnet\tools\al.exe")
PROJECT_DIR = os.path.dirname(os.path.abspath(__file__))
PKG_CACHE = os.path.join(PROJECT_DIR, ".alpackages")


def log(m):
    print(f"[{time.strftime('%H:%M:%S')}] {m}", flush=True)


def main():
    cmd = [AL_BIN, "launchmcpserver",
           "--transport", "stdio",
           "--packagecachepath", PKG_CACHE,
           "--nolog",
           PROJECT_DIR]
    log(f"cmd: {' '.join(cmd)}")
    proc = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, cwd=PROJECT_DIR)
    log(f"pid={proc.pid}")

    q: queue.Queue = queue.Queue()

    def reader():
        buf = b""
        while True:
            try:
                ch = proc.stdout.read(1)
                if not ch:
                    return
                buf += ch
                if buf.endswith(b"\n"):
                    line = buf.decode("utf-8", errors="replace").strip()
                    buf = b""
                    if line:
                        q.put(line)
            except Exception as e:
                log(f"reader err: {e}")
                return

    def stderr_reader():
        for line in iter(proc.stderr.readline, b""):
            log(f"STDERR: {line.decode('utf-8', errors='replace').rstrip()}")

    threading.Thread(target=reader, daemon=True).start()
    threading.Thread(target=stderr_reader, daemon=True).start()

    def send(msg):
        c = json.dumps(msg) + "\n"
        proc.stdin.write(c.encode("utf-8"))
        proc.stdin.flush()

    def recv(timeout=15):
        try:
            line = q.get(timeout=timeout)
            return json.loads(line)
        except Exception:
            return None

    send({"jsonrpc": "2.0", "id": 1, "method": "initialize",
          "params": {"protocolVersion": "2024-11-05",
                     "capabilities": {},
                     "clientInfo": {"name": "probe", "version": "0"}}})
    log(f"initialize -> {json.dumps(recv())[:400]}")
    send({"jsonrpc": "2.0", "method": "notifications/initialized", "params": {}})

    send({"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}})
    tools = recv(timeout=30)
    if not tools:
        log("NO tools/list response")
    else:
        result = tools.get("result", {})
        tlist = result.get("tools", [])
        log(f"tool count: {len(tlist)}")
        out = Path(PROJECT_DIR) / "ms-official-mcp-tools.json"
        out.write_text(json.dumps(result, indent=2))
        log(f"WROTE {out}")
        for t in tlist:
            log(f"  - {t.get('name')}: {t.get('description', '')[:90]}")

    send({"jsonrpc": "2.0", "id": 3, "method": "resources/list", "params": {}})
    r = recv(timeout=10)
    if r and r.get("result", {}).get("resources"):
        log(f"resources: {len(r['result']['resources'])}")
        for res in r["result"]["resources"][:20]:
            log(f"  - {res.get('uri')}: {res.get('name', '')}")

    send({"jsonrpc": "2.0", "id": 4, "method": "prompts/list", "params": {}})
    p = recv(timeout=10)
    if p and p.get("result", {}).get("prompts"):
        log(f"prompts: {len(p['result']['prompts'])}")

    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()


if __name__ == "__main__":
    main()
