#!/usr/bin/env python3
"""
End-to-end test for AL_LSP_SOURCE_ROOTS (source project references).

Fixture fixtures/source-refs: A -> B -> C and A -> C, no .alpackages anywhere.
A also references a codeunit that exists nowhere.

With AL_LSP_SOURCE_ROOTS pointing at the fixture, symbols from B and C must
resolve from source, and diagnostics for A must still be published and name
only the missing codeunit. The second point is the regression check for
nested closures: without one reference config per (reference, parent) pair,
the AL LS silently stops publishing diagnostics.

Without the variable, nothing resolves (control run).

Usage: python test_source_refs.py [--wrapper PATH] [--no-build]
"""

import argparse
import io
import json
import os
import queue
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path

if sys.platform == "win32":
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8", errors="replace")

HERE = Path(__file__).resolve().parent
FIXTURE = HERE / "fixtures" / "source-refs"
A = FIXTURE / "A"
A_FILE = A / "src" / "ATop.Codeunit.al"
GO_DIR = HERE.parent / "al-language-server-go"
EXE = "al-lsp-wrapper.exe" if sys.platform == "win32" else "al-lsp-wrapper"
DEFAULT_WRAPPER = GO_DIR / "bin" / EXE


class Wrapper:
    def __init__(self, exe, env):
        self.proc = subprocess.Popen([str(exe)], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                     stderr=subprocess.DEVNULL, cwd=str(A), env=env)
        self.rid = 0
        self.pending = {}
        self.lock = threading.Lock()
        self.diags = {}
        threading.Thread(target=self._read, daemon=True).start()

    def _read(self):
        out = self.proc.stdout
        while True:
            headers = {}
            while True:
                line = out.readline()
                if not line:
                    return
                line = line.decode("utf-8")
                if line in ("\r\n", "\n"):
                    break
                k, _, v = line.partition(":")
                headers[k.strip().lower()] = v.strip()
            msg = json.loads(out.read(int(headers.get("content-length", 0))).decode("utf-8"))
            if "id" in msg and "method" not in msg:
                slot = self.pending.pop(msg["id"], None)
                if slot:
                    slot.put(msg)
            elif msg.get("method") == "textDocument/publishDiagnostics":
                p = msg["params"]
                # Keyed by exact URI: al-sem publishes a differently-cased URI on
                # Windows, and only the AL LS form carries the compiler errors.
                self.diags[p["uri"].replace("%3A", ":").replace("%3a", ":")] = p["diagnostics"]
            elif "id" in msg:  # server -> client request
                self.send({"jsonrpc": "2.0", "id": msg["id"], "result": None})

    def send(self, msg):
        data = json.dumps(msg).encode("utf-8")
        with self.lock:
            self.proc.stdin.write(b"Content-Length: %d\r\n\r\n" % len(data) + data)
            self.proc.stdin.flush()

    def request(self, method, params, timeout=90):
        slot = queue.Queue()
        with self.lock:
            self.rid += 1
            rid = self.rid
            self.pending[rid] = slot
        self.send({"jsonrpc": "2.0", "id": rid, "method": method, "params": params})
        try:
            return slot.get(timeout=timeout)
        except queue.Empty:
            return {"error": {"message": f"timeout after {timeout}s"}}

    def notify(self, method, params):
        self.send({"jsonrpc": "2.0", "method": method, "params": params})

    def stop(self):
        try:
            self.request("shutdown", None, timeout=10)
            self.notify("exit", None)
            self.proc.wait(timeout=10)
        except Exception:
            self.proc.kill()


def locate(token, anchor):
    lines = A_FILE.read_text(encoding="utf-8").split("\n")
    for i, line in enumerate(lines):
        if anchor in line:
            return {"line": i, "character": line.index(token) + 2}
    raise ValueError(anchor)


def run(exe, source_roots):
    env = dict(os.environ)
    env.pop("AL_LSP_SOURCE_ROOTS", None)
    env.pop("AL_LSP_PACKAGE_CACHE", None)
    if source_roots:
        env["AL_LSP_SOURCE_ROOTS"] = source_roots
    w = Wrapper(exe, env)
    out = {"pid": w.proc.pid}
    try:
        root_uri = A.as_uri()
        r = w.request("initialize", {"processId": os.getpid(), "rootUri": root_uri, "rootPath": str(A),
                                     "capabilities": {}, "workspaceFolders": [{"uri": root_uri, "name": "A"}]})
        if "result" not in r:
            raise RuntimeError(f"initialize failed: {r}")
        w.notify("initialized", {})
        uri = A_FILE.as_uri()
        w.notify("textDocument/didOpen", {"textDocument": {
            "uri": uri, "languageId": "al", "version": 1, "text": A_FILE.read_text(encoding="utf-8")}})
        doc = {"uri": uri}

        r = w.request("textDocument/hover", {"textDocument": doc, "position": locate('"C Lib"', 'Lib: Codeunit')})
        c = (r.get("result") or {}).get("contents")
        out["hover"] = (c.get("value") if isinstance(c, dict) else c) or None

        r = w.request("textDocument/definition", {"textDocument": doc, "position": locate("Mid()", "exit(Mid.Mid())")})
        res = r.get("result") or []
        res = [res] if isinstance(res, dict) else res
        out["definition"] = [x.get("uri") or x.get("targetUri") for x in res]

        key = uri
        deadline = time.time() + 60
        while time.time() < deadline and not w.diags.get(key):
            time.sleep(1)
        out["diagnostics"] = [d.get("message", "") for d in w.diags.get(key, []) if d.get("severity") == 1]
    finally:
        w.stop()
    return out


def wrapper_log(pid):
    tmp = os.environ.get("TEMP") if sys.platform == "win32" else "/tmp"
    p = Path(tmp or tempfile.gettempdir()) / f"al-lsp-wrapper-go-{pid}.log"
    return p.read_text(encoding="utf-8", errors="replace") if p.exists() else ""


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--wrapper", default=str(DEFAULT_WRAPPER))
    ap.add_argument("--no-build", action="store_true")
    a = ap.parse_args()
    if not a.no_build:
        subprocess.run(["go", "build", "-trimpath", "-ldflags=-s -w", "-o", a.wrapper, "."], cwd=GO_DIR, check=True)

    failures = []

    def check(name, ok, detail):
        print(f"  [{'+' if ok else 'X'}] {'PASS' if ok else 'FAIL'}: {name} - {detail}")
        if not ok:
            failures.append(name)

    print("--- with AL_LSP_SOURCE_ROOTS ---")
    on = run(a.wrapper, str(FIXTURE))
    check("hover resolves C from source", bool(on["hover"]) and "C Lib" in on["hover"], repr(on["hover"])[:80])
    check("definition jumps into B source", any("/B/src/" in (u or "").replace("%20", " ") for u in on["definition"]),
          on["definition"])
    check("diagnostics published for A", bool(on["diagnostics"]), on["diagnostics"])
    check("only the missing codeunit is reported",
          on["diagnostics"] and all("Does Not Exist" in m for m in on["diagnostics"]), on["diagnostics"])
    log = wrapper_log(on["pid"])
    n = log.count("source refs: configuring")
    check("one reference config per (reference, parent) pair", n == 3, f"{n} configs logged (want 3)")

    print("--- without AL_LSP_SOURCE_ROOTS (control) ---")
    off = run(a.wrapper, None)
    check("hover does not resolve C", not off["hover"], repr(off["hover"])[:80])
    check("C and B reported missing", any("C Lib" in m for m in off["diagnostics"])
          and any("B Mid" in m for m in off["diagnostics"]), off["diagnostics"])
    check("no source index built", "source index" not in wrapper_log(off["pid"]), "log has no source index lines")

    print(f"\n{'OK' if not failures else 'FAILED'}: {len(failures)} failure(s)")
    sys.exit(1 if failures else 0)


if __name__ == "__main__":
    main()
