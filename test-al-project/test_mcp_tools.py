#!/usr/bin/env python3
"""Integration check: drive the wrapper's al/symbolRelations + al/inspectPage.

Gated: requires an almcp backend (nuget al tool or extension-bundled almcp).
Run manually:  python test_mcp_tools.py
"""
import json, subprocess, sys, os, io, threading, queue, time

if sys.platform == "win32":
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8", errors="replace")

PD = os.path.dirname(os.path.abspath(__file__))
WRAPPER = os.path.join(PD, "..", "al-language-server-go-windows", "bin", "al-lsp-wrapper.exe")


def main():
    if not os.path.exists(WRAPPER):
        print("SKIP: wrapper binary not built")
        return
    proc = subprocess.Popen([WRAPPER], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, cwd=PD)
    q = queue.Queue()

    def reader():
        buf = b""
        while True:
            ch = proc.stdout.read(1)
            if not ch:
                return
            buf += ch
            if b"\r\n\r\n" in buf:
                header, _, rest = buf.partition(b"\r\n\r\n")
                n = int([h.split(b":")[1] for h in header.split(b"\r\n")
                         if h.lower().startswith(b"content-length")][0])
                while len(rest) < n:
                    rest += proc.stdout.read(n - len(rest))
                q.put(json.loads(rest.decode("utf-8", "replace")))
                buf = b""

    threading.Thread(target=reader, daemon=True).start()

    def send(m):
        c = json.dumps(m)
        proc.stdin.write(f"Content-Length: {len(c)}\r\n\r\n{c}".encode())
        proc.stdin.flush()

    def recv_id(expected, t=60):
        deadline = time.time() + t
        while time.time() < deadline:
            try:
                m = q.get(timeout=max(0.1, deadline - time.time()))
            except queue.Empty:
                return None
            if m.get("id") == expected:
                return m
            # ignore notifications / other ids
        return None

    root = "file:///" + PD.replace("\\", "/")
    send({"jsonrpc": "2.0", "id": 1, "method": "initialize",
          "params": {"processId": os.getpid(), "rootUri": root,
                     "capabilities": {}, "workspaceFolders": [{"uri": root, "name": "test"}]}})
    recv_id(1)
    send({"jsonrpc": "2.0", "method": "initialized", "params": {}})
    send({"jsonrpc": "2.0", "id": 2, "method": "al/symbolRelations",
          "params": {"symbolName": "Customer", "symbolKind": "Table"}})
    print("symbolRelations:", json.dumps(recv_id(2, 60))[:400])
    send({"jsonrpc": "2.0", "id": 3, "method": "al/inspectPage",
          "params": {"pageName": "Customer Card", "content": "Controls"}})
    print("inspectPage:", json.dumps(recv_id(3, 60))[:400])
    proc.terminate()


if __name__ == "__main__":
    main()
