#!/usr/bin/env python3
"""Probe Microsoft's official `al launchlspserver` and dump ServerCapabilities."""
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


class LSP:
    def __init__(self, cmd):
        self.cmd = cmd
        self.proc = None
        self.q = queue.Queue()
        self.rid = 0

    def start(self):
        self.proc = subprocess.Popen(
            self.cmd,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            cwd=PROJECT_DIR,
        )
        log(f"started pid={self.proc.pid}")
        threading.Thread(target=self._reader, daemon=True).start()
        threading.Thread(target=self._stderr, daemon=True).start()

    def _reader(self):
        while True:
            try:
                headers = {}
                while True:
                    line = self.proc.stdout.readline().decode("utf-8", errors="replace")
                    if not line:
                        return
                    if line in ("\r\n", "\n"):
                        break
                    if ":" in line:
                        k, v = line.split(":", 1)
                        headers[k.strip()] = v.strip()
                if "Content-Length" not in headers:
                    continue
                n = int(headers["Content-Length"])
                body = self.proc.stdout.read(n).decode("utf-8", errors="replace")
                self.q.put(json.loads(body))
            except Exception as e:
                log(f"reader err: {e}")
                return

    def _stderr(self):
        for line in iter(self.proc.stderr.readline, b""):
            log(f"STDERR: {line.decode('utf-8', errors='replace').rstrip()}")

    def send(self, m):
        c = json.dumps(m)
        self.proc.stdin.write(f"Content-Length: {len(c)}\r\n\r\n{c}".encode("utf-8"))
        self.proc.stdin.flush()

    def request(self, method, params, timeout=30):
        self.rid += 1
        rid = self.rid
        self.send({"jsonrpc": "2.0", "id": rid, "method": method, "params": params})
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                msg = self.q.get(timeout=deadline - time.time())
            except queue.Empty:
                return None
            if msg.get("id") == rid:
                return msg
            if "method" in msg and "id" in msg:
                self.send({"jsonrpc": "2.0", "id": msg["id"], "result": None})
        return None

    def notify(self, method, params):
        self.send({"jsonrpc": "2.0", "method": method, "params": params})


def main():
    cmd = [AL_BIN, "launchlspserver", PROJECT_DIR,
           "--packagecachepath", PKG_CACHE,
           "--loglevel", "Verbose",
           "--disableTelemetry"]
    log(f"cmd: {' '.join(cmd)}")
    lsp = LSP(cmd)
    lsp.start()

    root_uri = "file:///" + PROJECT_DIR.replace("\\", "/")
    resp = lsp.request("initialize", {
        "processId": os.getpid(),
        "rootUri": root_uri,
        "capabilities": {
            "workspace": {"workspaceFolders": True, "configuration": True},
            "textDocument": {
                "synchronization": {"didSave": True, "willSave": False},
                "definition": {"dynamicRegistration": False, "linkSupport": True},
                "hover": {"dynamicRegistration": False, "contentFormat": ["markdown", "plaintext"]},
                "documentSymbol": {"dynamicRegistration": False, "hierarchicalDocumentSymbolSupport": True},
                "references": {"dynamicRegistration": False},
                "rename": {"dynamicRegistration": False, "prepareSupport": True},
                "codeAction": {"dynamicRegistration": False},
                "codeLens": {"dynamicRegistration": False},
                "completion": {"dynamicRegistration": False},
                "formatting": {"dynamicRegistration": False},
                "callHierarchy": {"dynamicRegistration": False},
                "typeHierarchy": {"dynamicRegistration": False},
                "inlayHint": {"dynamicRegistration": False},
                "foldingRange": {"dynamicRegistration": False},
                "semanticTokens": {"dynamicRegistration": False, "requests": {"full": True}},
                "publishDiagnostics": {"relatedInformation": True, "versionSupport": True},
            },
        },
        "workspaceFolders": [{"uri": root_uri, "name": "test-al-project"}],
    }, timeout=60)
    if not resp:
        log("NO RESPONSE to initialize")
        sys.exit(1)
    caps = resp.get("result", {}).get("capabilities", {})
    server_info = resp.get("result", {}).get("serverInfo", {})
    out = Path(PROJECT_DIR) / "ms-official-lsp-capabilities.json"
    out.write_text(json.dumps({
        "serverInfo": server_info,
        "capabilities": caps,
    }, indent=2))
    log(f"WROTE {out}")
    log(f"serverInfo: {json.dumps(server_info)}")
    log(f"top-level capability keys: {sorted(caps.keys())}")

    lsp.notify("initialized", {})
    time.sleep(2)
    lsp.notify("exit", None)
    try:
        lsp.proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        lsp.proc.kill()


if __name__ == "__main__":
    main()
