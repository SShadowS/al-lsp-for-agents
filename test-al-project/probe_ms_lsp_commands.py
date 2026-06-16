#!/usr/bin/env python3
"""Capture client/registerCapability + codeAction/codeLens commands from MS AL LSP."""
import json, subprocess, sys, os, io, threading, queue, time
from pathlib import Path

if sys.platform == 'win32':
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')
    sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8', errors='replace')

AL_BIN = os.path.expanduser(r"~\.dotnet\tools\al.exe")
PROJECT_DIR = os.path.dirname(os.path.abspath(__file__))
PKG_CACHE = os.path.join(PROJECT_DIR, ".alpackages")
SRC = os.path.join(PROJECT_DIR, "src")


def log(m): print(f"[{time.strftime('%H:%M:%S')}] {m}", flush=True)


def first_al_file():
    for root, _, files in os.walk(SRC):
        for f in files:
            if f.endswith(".al"):
                return os.path.join(root, f)
    return None


class LSP:
    def __init__(self, cmd):
        self.cmd = cmd; self.proc = None; self.q = queue.Queue(); self.rid = 0
        self.commands_registered = []
        self.requests_from_server = []

    def start(self):
        self.proc = subprocess.Popen(self.cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                     stderr=subprocess.PIPE, cwd=PROJECT_DIR)
        threading.Thread(target=self._reader, daemon=True).start()
        threading.Thread(target=self._stderr, daemon=True).start()

    def _reader(self):
        assert self.proc and self.proc.stdout
        while True:
            try:
                headers = {}
                while True:
                    line = self.proc.stdout.readline().decode("utf-8", errors="replace")
                    if not line: return
                    if line in ("\r\n", "\n"): break
                    if ":" in line:
                        k, v = line.split(":", 1); headers[k.strip()] = v.strip()
                if "Content-Length" not in headers: continue
                n = int(headers["Content-Length"])
                body = self.proc.stdout.read(n).decode("utf-8", errors="replace")
                msg = json.loads(body)
                if "method" in msg and "id" in msg:
                    self.requests_from_server.append(msg)
                    if msg["method"] == "client/registerCapability":
                        for reg in msg.get("params", {}).get("registrations", []):
                            log(f"  REGISTER: method={reg.get('method')} id={reg.get('id')} opts={json.dumps(reg.get('registerOptions', {}))[:200]}")
                            if reg.get("method") == "workspace/executeCommand":
                                cmds = reg.get("registerOptions", {}).get("commands", [])
                                self.commands_registered.extend(cmds)
                    # auto-respond to server requests
                    self._send({"jsonrpc": "2.0", "id": msg["id"], "result": None})
                self.q.put(msg)
            except Exception as e:
                log(f"reader err: {e}"); return

    def _stderr(self):
        assert self.proc and self.proc.stderr
        for line in iter(self.proc.stderr.readline, b""):
            s = line.decode("utf-8", errors="replace").rstrip()
            if s: log(f"STDERR: {s}")

    def _send(self, m):
        assert self.proc and self.proc.stdin
        c = json.dumps(m)
        self.proc.stdin.write(f"Content-Length: {len(c)}\r\n\r\n{c}".encode("utf-8"))
        self.proc.stdin.flush()

    def request(self, method, params, timeout=30):
        self.rid += 1; rid = self.rid
        self._send({"jsonrpc": "2.0", "id": rid, "method": method, "params": params})
        end = time.time() + timeout
        while time.time() < end:
            try:
                msg = self.q.get(timeout=max(0.1, end - time.time()))
            except queue.Empty:
                return None
            if msg.get("id") == rid and "method" not in msg:
                return msg
        return None

    def notify(self, method, params):
        self._send({"jsonrpc": "2.0", "method": method, "params": params})


def main():
    cmd = [AL_BIN, "launchlspserver", "--packagecachepath", PKG_CACHE, "--nolog", PROJECT_DIR]
    log(f"cmd: {' '.join(cmd)}")
    lsp = LSP(cmd); lsp.start()

    root_uri = "file:///" + PROJECT_DIR.replace("\\", "/")
    init = lsp.request("initialize", {
        "processId": os.getpid(), "rootUri": root_uri,
        "capabilities": {
            "workspace": {
                "workspaceFolders": True, "configuration": True,
                "executeCommand": {"dynamicRegistration": True},
                "didChangeConfiguration": {"dynamicRegistration": True},
            },
            "textDocument": {
                "synchronization": {"didSave": True},
                "codeAction": {"dynamicRegistration": False, "codeActionLiteralSupport": {"codeActionKind": {"valueSet": ["", "quickfix", "refactor", "refactor.extract", "refactor.inline", "refactor.rewrite", "source", "source.organizeImports"]}}},
                "codeLens": {"dynamicRegistration": False},
            },
        },
        "workspaceFolders": [{"uri": root_uri, "name": "test-al-project"}],
    }, timeout=60)
    if not init:
        log("NO init response"); sys.exit(1)
    server_caps = init.get("result", {}).get("capabilities", {})
    log(f"executeCommandProvider in init: {server_caps.get('executeCommandProvider')!r}")

    lsp.notify("initialized", {})
    time.sleep(3)

    # Open a real AL file and probe code actions + lenses
    f = first_al_file()
    if f:
        uri = "file:///" + f.replace("\\", "/")
        text = Path(f).read_text(encoding="utf-8", errors="replace")
        log(f"opening {f}")
        lsp.notify("textDocument/didOpen", {"textDocument": {"uri": uri, "languageId": "al", "version": 1, "text": text}})
        time.sleep(3)

        lens = lsp.request("textDocument/codeLens", {"textDocument": {"uri": uri}}, timeout=15)
        if lens and lens.get("result"):
            log(f"codeLens count: {len(lens['result'])}")
            for cl in lens["result"][:20]:
                c = cl.get("command")
                if c: log(f"  codeLens.command: {c.get('command')} title={c.get('title','')[:60]}")
        else:
            log("codeLens: empty/null")

        # try code actions over first 20 lines
        ca = lsp.request("textDocument/codeAction", {
            "textDocument": {"uri": uri},
            "range": {"start": {"line": 0, "character": 0}, "end": {"line": 20, "character": 0}},
            "context": {"diagnostics": [], "only": ["quickfix", "refactor", "source"]},
        }, timeout=15)
        if ca and ca.get("result"):
            log(f"codeAction count: {len(ca['result'])}")
            cmds = set()
            for a in ca["result"][:30]:
                if isinstance(a, dict):
                    if "command" in a and isinstance(a["command"], dict):
                        cmds.add(a["command"].get("command"))
                    if "kind" in a:
                        log(f"  codeAction kind={a.get('kind')} title={a.get('title','')[:60]}")
            for c in sorted(x for x in cmds if x):
                log(f"  codeAction.command: {c}")
        else:
            log("codeAction: empty/null")

    log(f"TOTAL registered commands via client/registerCapability: {len(lsp.commands_registered)}")
    for c in sorted(set(lsp.commands_registered)):
        log(f"  - {c}")

    out = Path(PROJECT_DIR) / "ms-lsp-server-requests.json"
    out.write_text(json.dumps(lsp.requests_from_server, indent=2))
    log(f"WROTE {out} (all server->client requests)")

    lsp.notify("exit", None)
    try:
        lsp.proc and lsp.proc.wait(timeout=3)
    except subprocess.TimeoutExpired:
        lsp.proc and lsp.proc.kill()


if __name__ == "__main__":
    main()
