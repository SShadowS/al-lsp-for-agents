#!/usr/bin/env python3
"""
Probe script: trace the path to discover events in a BC base codeunit.

Drives the wrapper directly and dumps all raw JSON-RPC to U:\tmp\probe-virtual-uri\.

Tests:
  1. initialize + didOpen a real BC project file
  2. textDocument/definition at SEVERAL positions (Codeunit ref, event name, etc.)
     until we capture an al-preview:/ URI from the AL LSP
  3. textDocument/documentSymbol on synthesized al-preview URIs for Approvals Mgmt.
     (covers the case where the agent already knows the codeunit name+id but
     hasn't navigated there yet)
  4. textDocument/documentSymbol on the virtual URI returned by definition,
     in multiple normalizations
  5. workspace/symbol with valid query (sanity check)
  6. hover + references on the virtual URI position
"""

from __future__ import annotations
import json
import os
import subprocess
import sys
import time
import io
from pathlib import Path

if sys.platform == 'win32':
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')
    sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8', errors='replace')

WRAPPER = r"U:\Git\claude-code-lsps\al-language-server-go-windows\bin\al-lsp-wrapper.exe"
# Test/ has Base Application in its .alpackages, so the wrapper's
# al-call-hierarchy subprocess can index Approvals Mgmt. + 100s of other
# base codeunits and serve them via dependencyDocumentSymbol. Override via
# PROBE_PROJECT env var to point at Cloud/ etc. for testing the
# packageCachePaths-discovery fix end-to-end.
TEST_ROOT = os.environ.get(
    "PROBE_PROJECT",
    r"U:\Git\DO.Support-wi-75360\DocumentOutput\Test",
)
SUB_FILE_CANDIDATES = [
    os.path.join(TEST_ROOT, "Src", "Auth", "CDOABSAuthSettingsTests.Codeunit.al"),
    os.path.join(TEST_ROOT, "Al", "Codeunit", "Codeunit 6175310 CDO Subscribers.al"),
]
SUB_FILE = next((p for p in SUB_FILE_CANDIDATES if os.path.exists(p)), SUB_FILE_CANDIDATES[0])

# Phase B fixture lives in our repo (deterministic content, source-of-truth).
# The probe copies it into TEST_ROOT before exercising hover + documentSymbol
# enrichments, then removes it on exit so DO.Support stays clean.
PROBE_FIXTURE_SRC = os.path.join(
    os.path.dirname(os.path.abspath(__file__)), "fixtures", "phase_b_probe.al"
)
# Probe fixture lives wherever AL files do for the chosen project. Test/
# uses Src/, Cloud/ uses Al/. Pick whichever exists, fall back to project
# root if neither does.
PROBE_FIXTURE_DEST = next(
    (os.path.join(TEST_ROOT, sub, "probe_phase_b.al")
     for sub in ("Src", "Al")
     if os.path.isdir(os.path.join(TEST_ROOT, sub))),
    os.path.join(TEST_ROOT, "probe_phase_b.al"),
)

OUT_DIR = r"U:\tmp\probe-virtual-uri"
os.makedirs(OUT_DIR, exist_ok=True)

# Positions to attempt for goToDefinition (1-based line, 1-based col)
DEF_PROBES = [
    # On the "Environment Triggers" string inside Codeunit::"..."
    (31, 65, "EnvironmentTriggers codeunit ref"),
    # On the event-name string literal 'OnAfterCopy...'
    (31, 90, "OnAfterCopy event name literal"),
    # On 'OnAfterValidateEvent' for Sales Header
    (45, 85, "Sales Header OnAfterValidateEvent literal"),
    # On the table reference Database::"Sales Header"
    (45, 50, "Sales Header table ref"),
    # On Codeunit type in a var decl (line 48)
    (48, 30, "CDO Continia License Mgt codeunit decl"),
]

# Synthetic al-preview URIs to try (Approvals Mgmt. is Codeunit 1535 in Base Application)
SYNTH_URIS = [
    "al-preview:/allang/Base Application/Codeunit/1535/Approvals Mgmt..dal",
    "al-preview:/allang/Base Application/Codeunit/1535/Approvals%20Mgmt..dal",
    "al-preview:///allang/Base Application/Codeunit/1535/Approvals Mgmt..dal",
    "al-preview://allang/Base Application/Codeunit/1535/Approvals Mgmt..dal",
]


class LSP:
    def __init__(self):
        self.id = 0
        self.proc: subprocess.Popen | None = None
        self.notifications: list[dict] = []

    def start(self):
        self.proc = subprocess.Popen(
            [WRAPPER],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            cwd=TEST_ROOT,
        )

    def send(self, msg):
        assert self.proc and self.proc.stdin
        body = json.dumps(msg).encode("utf-8")
        head = f"Content-Length: {len(body)}\r\n\r\n".encode("ascii")
        self.proc.stdin.write(head + body)
        self.proc.stdin.flush()

    def recv(self):
        assert self.proc and self.proc.stdout
        headers = {}
        while True:
            line = self.proc.stdout.readline()
            if not line:
                return None
            line = line.decode("utf-8", "replace")
            if line in ("\r\n", "\n"):
                break
            if ":" in line:
                k, v = line.split(":", 1)
                headers[k.strip()] = v.strip()
        if "Content-Length" not in headers:
            return None
        n = int(headers["Content-Length"])
        body = self.proc.stdout.read(n).decode("utf-8", "replace")
        return json.loads(body)

    def request(self, method, params, retries=5000):
        self.id += 1
        rid = self.id
        self.send({"jsonrpc": "2.0", "id": rid, "method": method, "params": params})
        for _ in range(retries):
            msg = self.recv()
            if not msg:
                continue
            if msg.get("id") == rid and "method" not in msg:
                return msg
            if "method" in msg:
                self.notifications.append(msg)
                if "id" in msg:
                    # Reply to server-initiated requests with empty result so server doesn't stall.
                    self.send({"jsonrpc": "2.0", "id": msg["id"], "result": None})
        return None

    def notify(self, method, params):
        self.send({"jsonrpc": "2.0", "method": method, "params": params})

    def drain(self, seconds):
        """Read messages for a fixed window, replying to server requests."""
        assert self.proc and self.proc.stdout
        end = time.time() + seconds
        while time.time() < end:
            # Non-blocking check: peek via select on Windows isn't available for pipes,
            # so just do a short blocking read with a timeout via select-on-stdout.
            try:
                msg = self.recv()
            except Exception:
                msg = None
            if not msg:
                break
            if "method" in msg:
                self.notifications.append(msg)
                if "id" in msg:
                    self.send({"jsonrpc": "2.0", "id": msg["id"], "result": None})

    def stop(self):
        try:
            assert self.proc
            self.proc.terminate()
            self.proc.wait(timeout=5)
        except Exception:
            if self.proc:
                self.proc.kill()


def dump(name, payload):
    path = os.path.join(OUT_DIR, name + ".json")
    with open(path, "w", encoding="utf-8") as f:
        json.dump(payload, f, indent=2, default=str)
    print(f"  -> {path}")


def main():
    print(f"Wrapper: {WRAPPER}")
    print(f"Test root: {TEST_ROOT}")
    print(f"Output: {OUT_DIR}\n")

    # Stage Phase B fixture into TEST_ROOT (so AL LSP treats it as project code).
    import shutil
    if os.path.exists(PROBE_FIXTURE_SRC):
        shutil.copyfile(PROBE_FIXTURE_SRC, PROBE_FIXTURE_DEST)

    lsp = LSP()
    lsp.start()
    try:
        root_uri = Path(TEST_ROOT).as_uri()
        sub_uri = Path(SUB_FILE).as_uri()

        # 1. Initialize
        print("[1] initialize")
        init = lsp.request("initialize", {
            "processId": os.getpid(),
            "rootPath": TEST_ROOT,
            "rootUri": root_uri,
            "workspaceFolders": [{"uri": root_uri, "name": "DocumentOutputCloud"}],
            "capabilities": {
                "textDocument": {
                    "hover": {"dynamicRegistration": True, "contentFormat": ["markdown", "plaintext"]},
                    "definition": {"dynamicRegistration": True, "linkSupport": True},
                    "references": {"dynamicRegistration": True},
                    "documentSymbol": {"dynamicRegistration": True, "hierarchicalDocumentSymbolSupport": True},
                },
                "workspace": {"symbol": {"dynamicRegistration": True}},
            },
        })
        dump("01-initialize", init)
        if not init or "result" not in init:
            print("initialize failed"); return

        lsp.notify("initialized", {})
        print("  waiting 30s for AL LSP to load project + .alpackages...")
        time.sleep(30)

        # 2. didOpen
        print("[2] didOpen CDO Subscribers")
        with open(SUB_FILE, "r", encoding="utf-8", errors="replace") as f:
            text = f.read()
        lsp.notify("textDocument/didOpen", {
            "textDocument": {"uri": sub_uri, "languageId": "al", "version": 1, "text": text},
        })
        # Give AL LSP time to index the file
        time.sleep(4)

        # 3. goToDefinition probes
        virtual_uri = None
        virtual_pos = None
        for ln, ch, label in DEF_PROBES:
            print(f"[3] definition @ {ln}:{ch} ({label})")
            r = lsp.request("textDocument/definition", {
                "textDocument": {"uri": sub_uri},
                "position": {"line": ln - 1, "character": ch - 1},
            })
            dump(f"03-def-l{ln}c{ch}", {"label": label, "response": r})
            if r and r.get("result"):
                res = r["result"]
                target = res[0] if isinstance(res, list) and res else res if isinstance(res, dict) else None
                if target:
                    u = target.get("uri") or target.get("targetUri")
                    if u and u.startswith("al-preview"):
                        virtual_uri = u
                        virtual_pos = (target.get("range") or target.get("targetSelectionRange") or target.get("targetRange") or {}).get("start")
                        print(f"  *** got virtual URI: {u}")
                        break
                    elif u:
                        print(f"  -> file URI: {u} (not al-preview)")

        # 4. documentSymbol on captured virtual URI
        if virtual_uri:
            print(f"[4] documentSymbol on captured virtual URI")
            for tag, uri in [
                ("verbatim", virtual_uri),
                ("backslashed", virtual_uri.replace("/", "\\")),
                ("triple-slash", virtual_uri.replace("al-preview:/", "al-preview:///", 1)),
                ("double-slash", virtual_uri.replace("al-preview:/", "al-preview://", 1)),
            ]:
                r = lsp.request("textDocument/documentSymbol", {"textDocument": {"uri": uri}})
                dump(f"04-docsym-{tag}", {"uri": uri, "response": r})

            # didOpen the virtual URI, then retry documentSymbol
            print("[4b] didOpen + documentSymbol on captured virtual URI")
            lsp.notify("textDocument/didOpen", {
                "textDocument": {"uri": virtual_uri, "languageId": "al", "version": 1, "text": ""},
            })
            time.sleep(1)
            r = lsp.request("textDocument/documentSymbol", {"textDocument": {"uri": virtual_uri}})
            dump("04b-docsym-after-open", {"uri": virtual_uri, "response": r})

            # hover + references on the virtual URI
            if virtual_pos:
                print(f"[5] hover on virtual URI at {virtual_pos}")
                r = lsp.request("textDocument/hover", {"textDocument": {"uri": virtual_uri}, "position": virtual_pos})
                dump("05-hover-virtual", r)

                print(f"[6] references on virtual URI at {virtual_pos}")
                r = lsp.request("textDocument/references", {
                    "textDocument": {"uri": virtual_uri}, "position": virtual_pos,
                    "context": {"includeDeclaration": False},
                })
                dump("06-refs-virtual", r)

        # 5. Synthetic al-preview URI probes for Approvals Mgmt. (Codeunit 1535)
        print("[7] documentSymbol on synthesized URIs for Approvals Mgmt.")
        for i, uri in enumerate(SYNTH_URIS):
            r = lsp.request("textDocument/documentSymbol", {"textDocument": {"uri": uri}})
            dump(f"07-synth-{i}", {"uri": uri, "response": r})

        # Also try didOpen-then-documentSymbol on synthesized URI
        synth = SYNTH_URIS[0]
        print(f"[7b] didOpen + documentSymbol on synth: {synth}")
        lsp.notify("textDocument/didOpen", {
            "textDocument": {"uri": synth, "languageId": "al", "version": 1, "text": ""},
        })
        time.sleep(1)
        r = lsp.request("textDocument/documentSymbol", {"textDocument": {"uri": synth}})
        dump("07b-synth-after-open", {"uri": synth, "response": r})

        # 6. workspace/symbol with various queries (positive control)
        print("[8] workspace/symbol queries")
        for q in ["Approvals Mgmt.", "Approvals", "OnAfterPostApprovalEntries", "OnPostApprovalEntries"]:
            r = lsp.request("workspace/symbol", {"query": q})
            count = 0
            if r and isinstance(r.get("result"), list):
                count = len(r["result"])
            print(f"  '{q}' -> {count} hits")
            dump(f"08-ws-{q.replace(' ','_').replace('.','')}", r)

        # 9. workspace/symbol with empty query — should return [] AND fire a
        # window/showMessage warning once so the user sees the Claude Code bug.
        print("[9] workspace/symbol with empty query (Phase B: [] + one-shot user warning)")
        notif_before = len(lsp.notifications)
        r = lsp.request("workspace/symbol", {"query": ""})
        dump("09-ws-empty", r)
        if r and r.get("result") == []:
            print("  [+] empty query -> []")
        else:
            print(f"  [-] unexpected response: {r}")
        # Notifications interleave with responses on the same stream; the
        # window/showMessage may arrive before or after this response. Make
        # a second non-empty request to drain anything queued behind it.
        _ = lsp.request("workspace/symbol", {"query": "Approvals Mgmt."})
        warning_seen = False
        for n in lsp.notifications[notif_before:]:
            if n.get("method") == "window/showMessage":
                msg = n.get("params", {}).get("message", "")
                if "workspaceSymbol" in msg and "Claude Code" in msg:
                    warning_seen = True
                    print(f"  [+] window/showMessage fired: {msg[:90]}...")
                    break
        if not warning_seen:
            print("  [-] expected window/showMessage warning, none seen")

        # 10. Phase B: hover on event-name literal in synthetic file
        probe_file = PROBE_FIXTURE_DEST
        if os.path.exists(probe_file):
            probe_uri = Path(probe_file).as_uri()
            with open(probe_file, "r", encoding="utf-8") as f:
                probe_text = f.read()
            lsp.notify("textDocument/didOpen", {
                "textDocument": {"uri": probe_uri, "languageId": "al", "version": 1, "text": probe_text},
            })
            time.sleep(2)

            # Cursor on the event name 'OnSendPurchaseDocForApproval'.
            # Fixture (0-based lines):
            #   0: codeunit 50199 "Phase B Probe"
            #   1: {
            #   2:     // Subscriber to a real Base Application event ...
            #   3:     [EventSubscriber(ObjectType::Codeunit, Codeunit::"Approvals Mgmt.", 'OnSendPurchaseDocForApproval', '', false, false)]
            # Column 84 lands inside the event-name string literal.
            print("[10] hover on event-name literal (Phase B enrichment)")
            r = lsp.request("textDocument/hover", {
                "textDocument": {"uri": probe_uri},
                "position": {"line": 3, "character": 84},
            })
            dump("10-hover-event-ref", r)
            value = ""
            if r and r.get("result") and isinstance(r["result"].get("contents"), dict):
                value = r["result"]["contents"].get("value", "")
            print("  hover excerpt:")
            for ln in value.splitlines()[:8]:
                print(f"    | {ln}")

            # 11. Phase B: documentSymbol overlay on local file
            print("[11] documentSymbol on local Phase B probe file")
            r = lsp.request("textDocument/documentSymbol", {
                "textDocument": {"uri": probe_uri},
            })
            dump("11-docsym-local", r)
            res = r.get("result") if r else None
            if isinstance(res, list):
                events = []
                def walk(syms):
                    for s in syms:
                        if s.get("kind") == 24:
                            events.append(s)
                        for c in (s.get("children") or []):
                            walk([c])
                walk(res)
                print(f"  total symbols: {len(res)}, event-kind: {len(events)}")
                for e in events[:5]:
                    print(f"    Event: name={e.get('name')!r}  detail={(e.get('detail') or '')[:90]}")

        dump("99-notifications-tail", lsp.notifications[-50:])

    finally:
        lsp.stop()
        # Remove the staged fixture so DO.Support stays clean between runs.
        if os.path.exists(PROBE_FIXTURE_DEST):
            try:
                os.remove(PROBE_FIXTURE_DEST)
            except OSError:
                pass
        print(f"\nAll output in {OUT_DIR}")


if __name__ == "__main__":
    main()
