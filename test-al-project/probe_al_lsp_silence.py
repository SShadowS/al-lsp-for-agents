#!/usr/bin/env python3
"""
Direct AL LSP probe — bypasses the wrapper, talks to Microsoft's AL Language
Server directly. Goal: characterize the silent-swallow behavior where
al/gotodefinition and al/symbolSearch never return a response on heavily-loaded
dependency trees.

Test matrix:
  scenario A: cold start, send al/gotodefinition immediately after initialize
              (no project init)
  scenario B: full init sequence (loadManifest + setActiveWorkspace +
              hasProjectClosureLoadedRequest), then send al/gotodefinition
  scenario C: same as B but ALSO poll hasProjectClosureLoaded until true,
              with a generous timeout, THEN send the gotodefinition
  scenario D: same as B but with longer warm-up window (60s wait after init)
  scenario E: al/symbolSearch under the same conditions as B

For each scenario, dump raw JSON-RPC traffic with timestamps to
U:\\tmp\\probe-al-lsp\\<scenario>.jsonl so we can correlate request/response
timing and identify when AL LSP stops responding.
"""

from __future__ import annotations
import json
import os
import subprocess
import sys
import threading
import time
import io
from pathlib import Path
from queue import Queue, Empty

if sys.platform == 'win32':
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')
    sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8', errors='replace')

AL_EXT = r"C:\Users\SShadowS\.vscode\extensions\ms-dynamics-smb.al-18.0.2293710"
AL_LSP_EXE = os.path.join(AL_EXT, "bin", "win32", "Microsoft.Dynamics.Nav.EditorServices.Host.exe")

# Project to probe — has Base Application + 30+ Continia apps in .alpackages.
# Override with env var PROBE_PROJECT to point at Cloud/ etc.
TEST_ROOT = os.environ.get(
    "PROBE_PROJECT",
    r"U:\Git\DO.Support-wi-75360\DocumentOutput\Test",
)
# Pick a file that exists in the chosen project root.
def _pick_test_file(root: str) -> str:
    for cand in (
        os.path.join(root, "Src", "Auth", "CDOABSAuthSettingsTests.Codeunit.al"),
        os.path.join(root, "Al", "Codeunit", "Codeunit 6175310 CDO Subscribers.al"),
    ):
        if os.path.exists(cand):
            return cand
    # Fall back: walk for any .al file.
    for dirpath, _, files in os.walk(root):
        for f in files:
            if f.endswith(".al"):
                return os.path.join(dirpath, f)
    return os.path.join(root, "missing.al")
TEST_FILE = _pick_test_file(TEST_ROOT)

OUT_DIR = r"U:\tmp\probe-al-lsp"
os.makedirs(OUT_DIR, exist_ok=True)


def now_ms() -> int:
    return int(time.time() * 1000)


def to_uri(path: str) -> str:
    return Path(path).as_uri()


class ALClient:
    """Minimal LSP client that talks to AL LSP and records every message.

    Records are written to <out_dir>/<scenario>.jsonl as one JSON-line per
    sent/received message with a timestamp and direction tag.
    """

    def __init__(self, scenario: str):
        self.scenario = scenario
        self.proc: subprocess.Popen | None = None
        self.id_counter = 0
        self.lock = threading.Lock()
        self.responses: dict[int, dict] = {}
        self.notifications: list[dict] = []
        self.server_requests: list[dict] = []
        self.reader_thread: threading.Thread | None = None
        self.recording_path = os.path.join(OUT_DIR, f"{scenario}.jsonl")
        # Truncate prior recording.
        open(self.recording_path, "w", encoding="utf-8").close()

    # ----- transport -----
    def _record(self, direction: str, msg: dict):
        entry = {"t": now_ms(), "dir": direction, "msg": msg}
        with open(self.recording_path, "a", encoding="utf-8") as f:
            f.write(json.dumps(entry) + "\n")

    def start(self):
        # Spawn AL LSP with verbose logging + dedicated log file so we can
        # observe what it's doing internally during a hang. CLI args mirror
        # what the VS Code AL extension passes (see al-vscode-extension skill).
        log_file = os.path.join(OUT_DIR, f"{self.scenario}-al-lsp.log")
        # Clear any prior log so we only see fresh activity.
        try:
            os.remove(log_file)
        except FileNotFoundError:
            pass
        args = [
            AL_LSP_EXE,
            f"/sessionId:{self.scenario}",
            "/logLevel:Verbose",
            # Try enabling the official workspace-symbol path too in case it
            # interacts with al/symbolSearch indexing.
            "/extendGoToSymbolInWorkspace:true",
            "/extendGoToSymbolInWorkspaceIncludeSymbolFiles:true",
        ]
        self.proc = subprocess.Popen(
            args,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            cwd=TEST_ROOT,
        )
        self.al_log_path = log_file
        self.stderr_path = os.path.join(OUT_DIR, f"{self.scenario}-al-lsp.stderr")
        # Drain stderr to a file so AL LSP doesn't block on a full pipe.
        self.stderr_thread = threading.Thread(
            target=self._drain_stderr, args=(self.stderr_path,), daemon=True
        )
        self.stderr_thread.start()
        self.reader_thread = threading.Thread(target=self._reader_loop, daemon=True)
        self.reader_thread.start()

    def _drain_stderr(self, path: str):
        assert self.proc and self.proc.stderr
        with open(path, "wb") as f:
            while True:
                chunk = self.proc.stderr.read(4096)
                if not chunk:
                    return
                f.write(chunk)
                f.flush()

    def _reader_loop(self):
        assert self.proc and self.proc.stdout
        while True:
            try:
                headers: dict[str, str] = {}
                while True:
                    line = self.proc.stdout.readline()
                    if not line:
                        return
                    line = line.decode("utf-8", "replace")
                    if line in ("\r\n", "\n"):
                        break
                    if ":" in line:
                        k, v = line.split(":", 1)
                        headers[k.strip()] = v.strip()
                if "Content-Length" not in headers:
                    continue
                n = int(headers["Content-Length"])
                body = self.proc.stdout.read(n).decode("utf-8", "replace")
                msg = json.loads(body)
                self._record("recv", msg)
                with self.lock:
                    if "id" in msg and "method" not in msg:
                        # Response to one of our requests.
                        self.responses[msg["id"]] = msg
                    elif "method" in msg and "id" in msg:
                        # Server-initiated request (must respond, else stalls).
                        self.server_requests.append(msg)
                    elif "method" in msg:
                        self.notifications.append(msg)
            except Exception as e:
                return

    def send(self, msg: dict):
        assert self.proc and self.proc.stdin
        self._record("send", msg)
        body = json.dumps(msg).encode("utf-8")
        head = f"Content-Length: {len(body)}\r\n\r\n".encode("ascii")
        self.proc.stdin.write(head + body)
        self.proc.stdin.flush()

    def request(self, method: str, params, timeout: float = 30.0) -> dict | None:
        with self.lock:
            self.id_counter += 1
            rid = self.id_counter
        self.send({"jsonrpc": "2.0", "id": rid, "method": method, "params": params})
        deadline = time.time() + timeout
        while time.time() < deadline:
            with self.lock:
                if rid in self.responses:
                    return self.responses.pop(rid)
                # Respond to any pending server requests so the server doesn't stall.
                pending = list(self.server_requests)
                self.server_requests.clear()
            for sr in pending:
                self.send({"jsonrpc": "2.0", "id": sr["id"], "result": None})
            time.sleep(0.02)
        return None  # timeout

    def notify(self, method: str, params):
        self.send({"jsonrpc": "2.0", "method": method, "params": params})

    def stop(self):
        try:
            if self.proc:
                self.proc.terminate()
                self.proc.wait(timeout=5)
        except Exception:
            if self.proc:
                self.proc.kill()


# ----- shared init pieces -----
def initialize(c: ALClient):
    root_uri = to_uri(TEST_ROOT)
    return c.request("initialize", {
        "processId": os.getpid(),
        "rootPath": TEST_ROOT,
        "rootUri": root_uri,
        "workspaceFolders": [{"uri": root_uri, "name": "Test"}],
        "capabilities": {
            "textDocument": {
                "hover": {"dynamicRegistration": True, "contentFormat": ["markdown"]},
                "definition": {"dynamicRegistration": True, "linkSupport": True},
                "documentSymbol": {"dynamicRegistration": True, "hierarchicalDocumentSymbolSupport": True},
            },
            "workspace": {"symbol": {"dynamicRegistration": True}},
        },
    }, timeout=60)


def load_manifest(c: ALClient):
    app_json_path = os.path.join(TEST_ROOT, "app.json")
    with open(app_json_path, "r", encoding="utf-8") as f:
        manifest_text = f.read()
    return c.request("al/loadManifest", {
        "projectFolder": TEST_ROOT,
        "manifest": manifest_text,
    }, timeout=120)


def set_active_workspace(c: ALClient):
    root_uri = to_uri(TEST_ROOT)
    settings = {
        "workspacePath": TEST_ROOT,
        "alResourceConfigurationSettings": {
            "assemblyProbingPaths": ["./.netpackages"],
            "codeAnalyzers": [],
            "enableCodeAnalysis": False,
            "backgroundCodeAnalysis": "None",
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
        "activeWorkspaceClosure": [TEST_ROOT],
    }
    return c.request("al/setActiveWorkspace", {
        "currentWorkspaceFolderPath": {"uri": root_uri, "name": "Test", "index": 0},
        "settings": settings,
    }, timeout=120)


def poll_closure_loaded(c: ALClient, max_wait_s: float = 120) -> bool:
    """Poll al/hasProjectClosureLoadedRequest until result.loaded is true.

    The wrapper sends `{"workspacePath": "..."}` (NOT a workspaceFolderPath
    object) and parses response as `{loaded: bool}`. Mirror exactly.
    """
    deadline = time.time() + max_wait_s
    poll = 0
    start = time.time()
    while time.time() < deadline:
        poll += 1
        resp = c.request("al/hasProjectClosureLoadedRequest", {
            "workspacePath": TEST_ROOT,
        }, timeout=10)
        if resp:
            res = resp.get("result")
            loaded = False
            if isinstance(res, dict):
                loaded = bool(res.get("loaded"))
            elif res is True:
                loaded = True
            if loaded:
                print(f"  [+] closure loaded after {poll} polls ({time.time()-start:.1f}s)")
                return True
        time.sleep(1)
    print(f"  [-] closure NEVER loaded after {max_wait_s}s ({poll} polls)")
    return False


def open_test_file(c: ALClient):
    with open(TEST_FILE, "r", encoding="utf-8") as f:
        text = f.read()
    c.notify("textDocument/didOpen", {
        "textDocument": {
            "uri": to_uri(TEST_FILE),
            "languageId": "al",
            "version": 1,
            "text": text,
        },
    })


def gotodefinition(c: ALClient, line: int, char: int, timeout: float = 30) -> dict | None:
    return c.request("al/gotodefinition", {
        "textDocument": {"uri": to_uri(TEST_FILE)},
        "position": {"line": line, "character": char},
    }, timeout=timeout)


def symbol_search(c: ALClient, query: str, timeout: float = 30) -> dict | None:
    return c.request("al/symbolSearch", {"query": query}, timeout=timeout)


# ----- scenarios -----
def scenario_A_cold():
    print("\n=== A: cold (no project init) ===")
    c = ALClient("A-cold")
    c.start()
    try:
        t0 = time.time()
        init = initialize(c)
        print(f"  initialize: {time.time()-t0:.1f}s, ok={init is not None}")

        c.notify("initialized", {})
        open_test_file(c)
        t0 = time.time()
        resp = gotodefinition(c, 0, 4, timeout=15)
        dt = time.time() - t0
        if resp is None:
            print(f"  gotodefinition: TIMEOUT after {dt:.1f}s — AL LSP swallowed it")
        elif resp.get("result") is None:
            print(f"  gotodefinition: null in {dt:.1f}s")
        else:
            print(f"  gotodefinition: result in {dt:.1f}s")
    finally:
        c.stop()


def scenario_B_init_then_gotodef():
    print("\n=== B: full init, then al/gotodefinition ===")
    c = ALClient("B-init-gotodef")
    c.start()
    try:
        initialize(c)
        c.notify("initialized", {})
        t0 = time.time()
        load_manifest(c)
        print(f"  loadManifest: {time.time()-t0:.1f}s")
        t0 = time.time()
        set_active_workspace(c)
        print(f"  setActiveWorkspace: {time.time()-t0:.1f}s")
        open_test_file(c)
        time.sleep(2)
        t0 = time.time()
        resp = gotodefinition(c, 0, 4, timeout=30)
        dt = time.time() - t0
        if resp is None:
            print(f"  gotodefinition: TIMEOUT after {dt:.1f}s — AL LSP swallowed it")
        else:
            res = resp.get("result")
            print(f"  gotodefinition: {dt:.1f}s, result={'<empty/null>' if not res else type(res).__name__}")
    finally:
        c.stop()


def scenario_C_wait_closure():
    print("\n=== C: full init + wait for closure loaded, then al/gotodefinition ===")
    c = ALClient("C-wait-closure")
    c.start()
    try:
        initialize(c)
        c.notify("initialized", {})
        load_manifest(c)
        set_active_workspace(c)
        open_test_file(c)
        print("  polling al/hasProjectClosureLoadedRequest...")
        poll_closure_loaded(c, max_wait_s=180)
        t0 = time.time()
        resp = gotodefinition(c, 0, 4, timeout=30)
        dt = time.time() - t0
        if resp is None:
            print(f"  gotodefinition: TIMEOUT after {dt:.1f}s — AL LSP swallowed it")
        else:
            res = resp.get("result")
            print(f"  gotodefinition: {dt:.1f}s, result={'<empty/null>' if not res else type(res).__name__}")
    finally:
        c.stop()


def scenario_D_long_warmup():
    print("\n=== D: full init + 60s sleep, then al/gotodefinition ===")
    c = ALClient("D-long-warmup")
    c.start()
    try:
        initialize(c)
        c.notify("initialized", {})
        load_manifest(c)
        set_active_workspace(c)
        open_test_file(c)
        print("  sleeping 60s for AL LSP to settle...")
        time.sleep(60)
        t0 = time.time()
        resp = gotodefinition(c, 0, 4, timeout=30)
        dt = time.time() - t0
        if resp is None:
            print(f"  gotodefinition: TIMEOUT after {dt:.1f}s — AL LSP swallowed it")
        else:
            res = resp.get("result")
            print(f"  gotodefinition: {dt:.1f}s, result={'<empty/null>' if not res else type(res).__name__}")
    finally:
        c.stop()


def set_active_workspace_with_extra_paths(c: ALClient, extra_paths: list[str]):
    """Same as set_active_workspace but with custom packageCachePaths.

    Used by scenario_G to test the hypothesis that adding the parent
    directory's .alpackages folder lets AL LSP resolve transitive deps
    and avoid the NRE crash.
    """
    root_uri = to_uri(TEST_ROOT)
    paths = ["./.alpackages"] + extra_paths
    settings = {
        "workspacePath": TEST_ROOT,
        "alResourceConfigurationSettings": {
            "assemblyProbingPaths": ["./.netpackages"],
            "codeAnalyzers": [],
            "enableCodeAnalysis": False,
            "backgroundCodeAnalysis": "None",
            "packageCachePaths": paths,
            "ruleSetPath": None,
            "enableCodeActions": True,
            "incrementalBuild": False,
            "outputAnalyzerStatistics": True,
            "enableExternalRulesets": True,
        },
        "setActiveWorkspace": True,
        "dependencyParentWorkspacePath": None,
        "expectedProjectReferenceDefinitions": [],
        "activeWorkspaceClosure": [TEST_ROOT],
    }
    print(f"  packageCachePaths = {paths}")
    return c.request("al/setActiveWorkspace", {
        "currentWorkspaceFolderPath": {"uri": root_uri, "name": "Test", "index": 0},
        "settings": settings,
    }, timeout=120)


def scenario_G_extra_packagecache():
    """Test the fix hypothesis: adding the parent dir's .alpackages to
    packageCachePaths lets AL LSP find transitive Microsoft .app files
    that aren't in the project's own .alpackages folder.
    """
    print("\n=== G: HYPOTHESIS — add parent .alpackages to packageCachePaths ===")
    c = ALClient("G-extra-paths")
    c.start()
    try:
        initialize(c)
        c.notify("initialized", {})
        load_manifest(c)

        # The Cloud project lives at DocumentOutput/Cloud/. The Microsoft
        # .app files we need are at DocumentOutput/.alpackages/. So we add
        # `../.alpackages` to packageCachePaths.
        set_active_workspace_with_extra_paths(c, ["../.alpackages"])

        open_test_file(c)
        time.sleep(2)
        for q in ["Approvals Mgmt.", "Customer", "Sales Header"]:
            t0 = time.time()
            resp = c.request("al/symbolSearch", {"query": q}, timeout=45)
            dt = time.time() - t0
            if resp is None:
                print(f"  symbolSearch({q!r}): TIMEOUT after {dt:.1f}s")
            else:
                res = resp.get("result")
                n = len(res.get("symbols", [])) if isinstance(res, dict) else 0
                print(f"  symbolSearch({q!r}): {dt:.1f}s, {n} symbols")
    finally:
        c.stop()


def scenario_F_stress():
    """Stress test: many requests in a row to measure response time degradation.

    Hypothesis: AL LSP's silent-swallow in the wrapper is response-time
    degradation under load — single-threaded request processing combined
    with the wrapper's 30s default timeout. If response times stay <30s
    even under repeated calls, the wrapper's timeout is the cause; if they
    drift toward infinity, AL LSP itself is the cause.
    """
    print("\n=== F: stress — repeated al/symbolSearch + al/gotodefinition ===")
    c = ALClient("F-stress")
    c.start()
    try:
        initialize(c)
        c.notify("initialized", {})
        load_manifest(c)
        set_active_workspace(c)
        open_test_file(c)
        poll_closure_loaded(c, max_wait_s=30)

        queries = [
            "Approvals Mgmt.", "Customer", "Sales Header", "Item",
            "GL Account", "Vendor", "Purchase Header", "Payment Method",
            "Currency", "Posting Group", "Approvals Mgmt.", "Customer",
        ]
        max_seen = 0.0
        for q in queries:
            t0 = time.time()
            resp = c.request("al/symbolSearch", {"query": q}, timeout=60)
            dt = time.time() - t0
            max_seen = max(max_seen, dt)
            status = "TIMEOUT" if resp is None else f"{(len(resp.get('result',{}).get('symbols',[])) if isinstance(resp.get('result'),dict) else 0)} hits"
            print(f"  symbolSearch({q!r:30}) {dt:5.1f}s  {status}")
        print(f"  worst-case latency: {max_seen:.1f}s")

        # Now hammer gotodefinition at varied positions.
        print("  ---")
        positions = [(0, 4), (5, 10), (10, 20), (15, 30), (20, 40), (3, 15), (8, 8)]
        for ln, ch in positions:
            t0 = time.time()
            resp = gotodefinition(c, ln, ch, timeout=45)
            dt = time.time() - t0
            status = "TIMEOUT" if resp is None else "got result" if resp.get("result") else "null"
            print(f"  gotodefinition l{ln} c{ch:3} {dt:5.1f}s  {status}")
    finally:
        c.stop()


def scenario_E_symbol_search():
    print("\n=== E: full init + closure loaded, then al/symbolSearch ===")
    c = ALClient("E-symbol-search")
    c.start()
    try:
        initialize(c)
        c.notify("initialized", {})
        load_manifest(c)
        set_active_workspace(c)
        open_test_file(c)
        print("  polling closure...")
        poll_closure_loaded(c, max_wait_s=180)
        for q in ["Approvals Mgmt.", "Customer"]:
            t0 = time.time()
            resp = symbol_search(c, q, timeout=30)
            dt = time.time() - t0
            if resp is None:
                print(f"  symbolSearch({q!r}): TIMEOUT after {dt:.1f}s")
            else:
                res = resp.get("result")
                n = len(res.get("symbols", [])) if isinstance(res, dict) else 0
                print(f"  symbolSearch({q!r}): {dt:.1f}s, {n} symbols")
    finally:
        c.stop()


if __name__ == "__main__":
    print(f"AL LSP: {AL_LSP_EXE}")
    print(f"Test project: {TEST_ROOT}")
    print(f"Recordings: {OUT_DIR}")

    only = sys.argv[1] if len(sys.argv) > 1 else None
    scenarios = {
        "A": scenario_A_cold,
        "B": scenario_B_init_then_gotodef,
        "C": scenario_C_wait_closure,
        "D": scenario_D_long_warmup,
        "E": scenario_E_symbol_search,
        "F": scenario_F_stress,
        "G": scenario_G_extra_packagecache,
    }
    if only:
        scenarios[only]()
    else:
        for name in ("A", "B", "C", "E"):  # skip D's 60s warmup unless asked
            scenarios[name]()
