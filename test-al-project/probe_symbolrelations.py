#!/usr/bin/env python3
"""Probe EditorServices al/symbolRelations + al/getApplicationObject to learn shapes."""
import json, subprocess, sys, os, io, glob, threading, queue, time
from pathlib import Path

if sys.platform == 'win32':
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')
    sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8', errors='replace')

PROJECT_DIR = os.path.dirname(os.path.abspath(__file__))
EXT_DIR = sorted(glob.glob(os.path.expanduser(r"~\.vscode\extensions\ms-dynamics-smb.al-*")))[-1]
EXE = os.path.join(EXT_DIR, "bin", "win32", "Microsoft.Dynamics.Nav.EditorServices.Host.exe")


def log(m): print(f"[{time.strftime('%H:%M:%S')}] {m}", flush=True)


class LSP:
    def __init__(self): self.proc=None; self.q=queue.Queue(); self.rid=0

    def start(self):
        self.proc = subprocess.Popen([EXE], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                     stderr=subprocess.PIPE, cwd=EXT_DIR)
        threading.Thread(target=self._reader, daemon=True).start()

    def _reader(self):
        while True:
            try:
                headers={}
                while True:
                    line=self.proc.stdout.readline().decode("utf-8","replace")
                    if not line: return
                    if line in ("\r\n","\n"): break
                    if ":" in line:
                        k,v=line.split(":",1); headers[k.strip()]=v.strip()
                if "Content-Length" not in headers: continue
                n=int(headers["Content-Length"])
                body=self.proc.stdout.read(n).decode("utf-8","replace")
                self.q.put(json.loads(body))
            except Exception as e:
                log(f"reader err: {e}"); return

    def send(self,m):
        c=json.dumps(m); self.proc.stdin.write(f"Content-Length: {len(c)}\r\n\r\n{c}".encode()); self.proc.stdin.flush()

    def req(self, method, params, timeout=30):
        self.rid+=1; rid=self.rid
        self.send({"jsonrpc":"2.0","id":rid,"method":method,"params":params})
        end=time.time()+timeout
        while time.time()<end:
            try: msg=self.q.get(timeout=max(0.1,end-time.time()))
            except queue.Empty: return None
            if msg.get("id")==rid and "method" not in msg: return msg
            if "method" in msg and "id" in msg:
                self.send({"jsonrpc":"2.0","id":msg["id"],"result":None})
        return None

    def notify(self, method, params): self.send({"jsonrpc":"2.0","method":method,"params":params})


def main():
    lsp=LSP(); lsp.start()
    root_uri="file:///"+PROJECT_DIR.replace("\\","/")
    lsp.req("initialize", {"processId":os.getpid(),"rootUri":root_uri,
            "capabilities":{"workspace":{"workspaceFolders":True,"configuration":True}},
            "initializationOptions":{},
            "workspaceFolders":[{"uri":root_uri,"name":"test-al-project"}]}, timeout=60)
    lsp.notify("initialized", {})
    sw=lsp.req("al/setActiveWorkspace", {"currentWorkspaceFolderPath":{"uri":root_uri,"name":"test-al-project"},
               "settings":{}}, timeout=30)
    log(f"setActiveWorkspace -> {json.dumps(sw)[:200]}")
    time.sleep(5)

    # Try several param shapes for al/symbolRelations
    shapes = [
        {"symbolName":"Customer","symbolKind":"Table"},
        {"name":"Customer","kind":"Table"},
        {"query":"Customer","kind":"Table"},
        {"symbolName":"Customer"},
    ]
    for s in shapes:
        r=lsp.req("al/symbolRelations", s, timeout=15)
        log(f"al/symbolRelations {json.dumps(s)} -> {json.dumps(r)[:500]}")

    # Probe getApplicationObject for page layout
    for s in [{"name":"Customer Card","kind":"Page"},{"objectName":"Customer Card","objectType":"Page"},
              {"name":"Customer Card"}]:
        r=lsp.req("al/getApplicationObject", s, timeout=15)
        log(f"al/getApplicationObject {json.dumps(s)} -> {json.dumps(r)[:500]}")

    lsp.notify("exit", None)
    try: lsp.proc.wait(timeout=5)
    except Exception: lsp.proc.kill()


if __name__ == "__main__":
    main()
