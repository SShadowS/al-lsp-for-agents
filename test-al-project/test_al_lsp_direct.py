#!/usr/bin/env python3
"""
Test the AL LSP directly (no Go wrapper) to investigate project loading behavior.
"""

import json
import subprocess
import sys
import os
import time
import io
import glob
import threading
import queue

if sys.platform == 'win32':
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')
    sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8', errors='replace')

from pathlib import Path

# Find AL LSP executable
AL_EXTENSION_BASE = os.path.expanduser(r"~\.vscode\extensions")
def find_al_extension():
    pattern = os.path.join(AL_EXTENSION_BASE, "ms-dynamics-smb.al-*")
    matches = glob.glob(pattern)
    if not matches:
        return None
    matches.sort(reverse=True)
    return matches[0]

AL_EXTENSION = find_al_extension()
AL_LSP_EXE = os.path.join(AL_EXTENSION, "bin", "win32", "Microsoft.Dynamics.Nav.EditorServices.Host.exe") if AL_EXTENSION else None


def log(msg):
    """Print with timestamp and flush."""
    ts = time.strftime("%H:%M:%S")
    print(f"[{ts}] {msg}", flush=True)

# Project to test
PROJECT_DIR = r"u:\Git\DO.Support-66858\DocumentOutput\Cloud"
TEST_TABLE_FILE = os.path.join(PROJECT_DIR, "Al", "Table", "Table 6175281 CDO Setup.al")
TEST_CODEUNIT_FILE = os.path.join(PROJECT_DIR, "Al", "Codeunit", "Codeunit 6175279 CDO Module Manager.al")


class DirectLSPClient:
    def __init__(self, executable, cwd):
        self.executable = executable
        self.cwd = cwd
        self.proc = None
        self.request_id = 0
        self._msg_queue = queue.Queue()
        self._reader_thread = None

    def start(self):
        self.proc = subprocess.Popen(
            [self.executable],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            cwd=self.cwd
        )
        log(f"  AL LSP started (PID: {self.proc.pid})")
        # Start single reader thread
        self._reader_thread = threading.Thread(target=self._read_loop, daemon=True)
        self._reader_thread.start()

    def _read_loop(self):
        """Single reader thread — reads messages and puts them on the queue."""
        while True:
            try:
                headers = {}
                while True:
                    line = self.proc.stdout.readline().decode("utf-8")
                    if not line:
                        return  # EOF
                    if line == "\r\n":
                        break
                    if ":" in line:
                        key, value = line.split(":", 1)
                        headers[key.strip()] = value.strip()
                if "Content-Length" not in headers:
                    continue
                content_length = int(headers["Content-Length"])
                content = self.proc.stdout.read(content_length).decode("utf-8")
                msg = json.loads(content)
                self._msg_queue.put(msg)
            except Exception as e:
                log(f"  reader thread error: {e}")
                return

    def stop(self):
        if self.proc:
            try:
                self.proc.terminate()
                self.proc.wait(timeout=5)
            except:
                self.proc.kill()

    def send(self, msg):
        content = json.dumps(msg)
        message = f"Content-Length: {len(content)}\r\n\r\n{content}"
        self.proc.stdin.write(message.encode("utf-8"))
        self.proc.stdin.flush()

    def receive(self, timeout=60):
        """Receive next message from queue with timeout."""
        try:
            return self._msg_queue.get(timeout=timeout)
        except queue.Empty:
            return None

    def request(self, method, params, max_retries=300):
        self.request_id += 1
        rid = self.request_id
        msg = {"jsonrpc": "2.0", "id": rid, "method": method, "params": params}
        self.send(msg)

        for _ in range(max_retries):
            response = self.receive()
            if not response:
                return rid, None
            if response.get("id") == rid:
                return rid, response
            # It's a notification or server request — log and handle
            if "method" in response:
                method_name = response["method"]
                if "id" in response:
                    # Server request — must respond
                    self.handle_server_request(response)
                else:
                    # Notification — just log interesting ones
                    if method_name not in ("textDocument/publishDiagnostics",):
                        log(f"  [notification] {method_name}")
        return rid, None

    def handle_server_request(self, req):
        method = req["method"]
        rid = req["id"]
        log(f"  [server request] {method} (id={rid})")

        if method == "workspace/configuration":
            # Return array of nulls
            items = req.get("params", {}).get("items", [])
            result = [None] * len(items)
            self.send({"jsonrpc": "2.0", "id": rid, "result": result})

            # Also send didChangeConfiguration for each workspace
            for item in items:
                scope_uri = item.get("scopeUri", "")
                if scope_uri:
                    ws_path = scope_uri.replace("file:///", "").replace("/", "\\")
                    # Fix drive letter
                    if len(ws_path) > 1 and ws_path[1] == ':':
                        pass  # already good
                    elif len(ws_path) > 2 and ws_path[0].isalpha() and ws_path[1] == '%':
                        ws_path = ws_path[0] + ':' + ws_path[2:]
                else:
                    ws_path = self.cwd

                settings = self._make_workspace_settings(ws_path)
                self.notify("workspace/didChangeConfiguration", {"settings": settings})

        elif method in ("client/registerCapability", "client/unregisterCapability",
                        "window/workDoneProgress/create"):
            self.send({"jsonrpc": "2.0", "id": rid, "result": None})

        elif method == "al/activeProjectLoaded":
            log(f"    >>> al/activeProjectLoaded received! params={json.dumps(req.get('params', {}))}")
            self.send({"jsonrpc": "2.0", "id": rid, "result": None})

        else:
            log(f"    (responding with null to {method})")
            self.send({"jsonrpc": "2.0", "id": rid, "result": None})

    def notify(self, method, params):
        msg = {"jsonrpc": "2.0", "method": method, "params": params}
        self.send(msg)

    def _make_workspace_settings(self, workspace_path):
        return {
            "workspacePath": workspace_path,
            "alResourceConfigurationSettings": {
                "assemblyProbingPaths": ["./.netpackages"],
                "codeAnalyzers": [],
                "enableCodeAnalysis": False,
                "backgroundCodeAnalysis": "Project",
                "packageCachePaths": ["./.alpackages"],
                "ruleSetPath": None,
                "enableCodeActions": True,
                "incrementalBuild": False,
                "outputAnalyzerStatistics": True,
                "enableExternalRulesets": True,
            },
            "setActiveWorkspace": True,
            "dependencyParentWorkspacePath": None,
            "expectedProjectReferenceDefinitions": [],
            "activeWorkspaceClosure": [workspace_path],
        }


def main():
    if not os.path.exists(PROJECT_DIR):
        log(f"SKIP: Project not found at {PROJECT_DIR}")
        sys.exit(0)

    if not AL_LSP_EXE or not os.path.exists(AL_LSP_EXE):
        log(f"SKIP: AL LSP not found at {AL_LSP_EXE}")
        sys.exit(0)

    log(f"AL LSP: {AL_LSP_EXE}")
    log(f"Project: {PROJECT_DIR}")
    log(f".alpackages exists: {os.path.exists(os.path.join(PROJECT_DIR, '.alpackages'))}")

    client = DirectLSPClient(AL_LSP_EXE, PROJECT_DIR)
    client.start()

    try:
        # === Step 1: Initialize ===
        log("--- Step 1: Initialize ---")
        root_uri = Path(PROJECT_DIR).as_uri()
        _, resp = client.request("initialize", {
            "processId": os.getpid(),
            "rootPath": PROJECT_DIR,
            "rootUri": root_uri,
            "capabilities": {
                "textDocument": {
                    "hover": {"dynamicRegistration": True},
                    "definition": {"dynamicRegistration": True},
                    "documentSymbol": {"dynamicRegistration": True},
                },
                "workspace": {
                    "symbol": {"dynamicRegistration": True},
                    "configuration": True,
                    "didChangeConfiguration": {"dynamicRegistration": True},
                }
            },
            "workspaceFolders": [
                {"uri": root_uri, "name": os.path.basename(PROJECT_DIR)}
            ]
        })
        if resp and "result" in resp:
            log("  Initialize: OK")
        else:
            log(f"  Initialize: FAILED - {resp}")
            return

        # === Step 2: initialized ===
        log("--- Step 2: Send initialized ---")
        client.notify("initialized", {})
        time.sleep(1)

        # Drain any pending notifications/requests
        log("  Draining notifications...")
        drain_count = 0
        while True:
            r = client.receive(timeout=5)
            if not r:
                break
            if "method" in r:
                if "id" in r:
                    client.handle_server_request(r)
                else:
                    if r["method"] not in ("textDocument/publishDiagnostics",):
                        log(f"  [notification] {r['method']}")
                drain_count += 1
            else:
                break
            if drain_count > 500:
                break
        log(f"  Drained {drain_count} messages")

        # === Step 3a: Open test file FIRST (like Go wrapper does) ===
        log("--- Step 3a: Open test file before project init ---")
        if os.path.exists(TEST_CODEUNIT_FILE):
            with open(TEST_CODEUNIT_FILE) as f:
                fc = f.read()
            client.notify("textDocument/didOpen", {
                "textDocument": {
                    "uri": Path(TEST_CODEUNIT_FILE).as_uri(),
                    "languageId": "al",
                    "version": 1,
                    "text": fc,
                }
            })
            log(f"  Opened {os.path.basename(TEST_CODEUNIT_FILE)}")
            # Drain any reactions
            time.sleep(1)
            dc = 0
            while True:
                r = client.receive(timeout=2)
                if not r:
                    break
                if "method" in r and "id" in r:
                    client.handle_server_request(r)
                dc += 1
                if dc > 200:
                    break
            log(f"  Drained {dc} messages after didOpen")

        # === Step 3b: Send workspace config ===
        log("--- Step 3b: Send workspace/didChangeConfiguration ---")
        settings = client._make_workspace_settings(PROJECT_DIR)
        client.notify("workspace/didChangeConfiguration", {"settings": settings})

        # === Step 4: Open app.json ===
        log("--- Step 4: Open app.json ---")
        app_json_path = os.path.join(PROJECT_DIR, "app.json")
        with open(app_json_path) as f:
            app_json_content = f.read()
        client.notify("textDocument/didOpen", {
            "textDocument": {
                "uri": Path(app_json_path).as_uri(),
                "languageId": "json",
                "version": 1,
                "text": app_json_content
            }
        })

        # === Step 5: al/loadManifest ===
        log("--- Step 5: al/loadManifest ---")
        _, resp = client.request("al/loadManifest", {
            "projectFolder": PROJECT_DIR,
            "manifest": app_json_content,
        })
        if resp:
            log(f"  loadManifest result: {json.dumps(resp.get('result', resp.get('error')))}")
        else:
            log("  loadManifest: no response")

        # === Step 6: al/setActiveWorkspace ===
        log("--- Step 6: al/setActiveWorkspace ---")
        manifest = json.loads(app_json_content)
        _, resp = client.request("al/setActiveWorkspace", {
            "currentWorkspaceFolderPath": {
                "uri": root_uri,
                "name": os.path.basename(PROJECT_DIR),
                "index": 0,
            },
            "settings": settings,
        })
        if resp:
            log(f"  setActiveWorkspace result: {json.dumps(resp.get('result', resp.get('error')))}")
        else:
            log("  setActiveWorkspace: no response")

        # === Step 7: Poll hasProjectClosureLoadedRequest ===
        log("--- Step 7: Poll hasProjectClosureLoaded (up to 5 min) ---")
        start = time.time()
        for i in range(300):  # up to 5 minutes
            _, resp = client.request("al/hasProjectClosureLoadedRequest", {
                "workspacePath": PROJECT_DIR,
            })
            if resp and "result" in resp:
                loaded = resp["result"]
                elapsed = time.time() - start
                if i < 3 or i % 10 == 0 or loaded:
                    log(f"  [{elapsed:.0f}s] poll {i}: raw result = {json.dumps(resp['result'])}")
                if isinstance(loaded, dict) and loaded.get("loaded"):
                    log(f"  PROJECT LOADED after {elapsed:.1f}s!")
                    break
                elif isinstance(loaded, bool) and loaded:
                    log(f"  PROJECT LOADED after {elapsed:.1f}s!")
                    break
            else:
                log(f"  [{time.time()-start:.0f}s] No response")
                break
            time.sleep(1)
        else:
            log(f"  TIMEOUT after {time.time()-start:.1f}s - project never loaded")

        # === Step 8: Test hover ===
        log("--- Step 8: Test hover ---")
        if os.path.exists(TEST_CODEUNIT_FILE):
            with open(TEST_CODEUNIT_FILE) as f:
                file_content = f.read()
            file_uri = Path(TEST_CODEUNIT_FILE).as_uri()

            client.notify("textDocument/didOpen", {
                "textDocument": {
                    "uri": file_uri,
                    "languageId": "al",
                    "version": 1,
                    "text": file_content,
                }
            })
            time.sleep(1)

            log("  Hover on line 1, char 10:")
            _, resp = client.request("textDocument/hover", {
                "textDocument": {"uri": file_uri},
                "position": {"line": 1, "character": 10},
            })
            if resp:
                log(f"  Hover raw result: {json.dumps(resp.get('result', resp.get('error')))}")
            else:
                log("  Hover: no response")

            # Also try hovering on a procedure name
            lines = file_content.split('\n')
            proc_line = None
            for idx, line in enumerate(lines):
                if 'procedure ' in line.lower():
                    proc_line = idx
                    break
            if proc_line is not None:
                log(f"  Hover on procedure line {proc_line}: {lines[proc_line].strip()[:80]}")
                _, resp = client.request("textDocument/hover", {
                    "textDocument": {"uri": file_uri},
                    "position": {"line": proc_line, "character": 20},
                })
                if resp:
                    log(f"  Hover raw result: {json.dumps(resp.get('result', resp.get('error')))}")
                else:
                    log("  Hover: no response")

        # === Step 9: Test documentSymbol ===
        log("--- Step 9: Test documentSymbol ---")
        if os.path.exists(TEST_TABLE_FILE):
            with open(TEST_TABLE_FILE) as f:
                file_content = f.read()
            file_uri = Path(TEST_TABLE_FILE).as_uri()

            client.notify("textDocument/didOpen", {
                "textDocument": {
                    "uri": file_uri,
                    "languageId": "al",
                    "version": 1,
                    "text": file_content,
                }
            })
            time.sleep(1)

            _, resp = client.request("textDocument/documentSymbol", {
                "textDocument": {"uri": file_uri},
            })
            if resp:
                result = resp.get("result")
                if result and isinstance(result, list):
                    log(f"  documentSymbol: got {len(result)} symbols")
                    if result:
                        log(f"  First symbol: {result[0].get('name', 'N/A')}")
                else:
                    log(f"  documentSymbol raw: {json.dumps(result)}")
            else:
                log("  documentSymbol: no response")

    finally:
        client.stop()
        log("--- Done ---")


if __name__ == "__main__":
    main()
