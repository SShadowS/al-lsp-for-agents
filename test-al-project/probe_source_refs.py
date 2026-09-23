#!/usr/bin/env python3
"""
Spike: can the Microsoft AL Language Server resolve cross-app symbols from
*source* (VS Code "project references") when there are no .alpackages?

Drives Microsoft.Dynamics.Nav.EditorServices.Host directly (no Go wrapper),
replaying the VS Code AL extension's multi-root flow:

  1. initialize with every closure folder as a workspace folder
  2. al/setActiveWorkspace for the active project, with
     activeWorkspaceClosure = transitive closure of source-resolvable deps and
     expectedProjectReferenceDefinitions = the deps that a folder satisfies
  3. on the server's al/activeProjectLoaded request, send
     workspace/didChangeConfiguration for each project reference with
     setActiveWorkspace=false and dependencyParentWorkspacePath=<parent>
     (ProjectDependencyHandlerService.loadProjectReferences in extension.js)

Then runs hover/definition/references probes and records load time, peak
memory and diagnostics.

Usage:
  python probe_source_refs.py --mode baseline --out result.json
  python probe_source_refs.py --mode refs --out result.json
  python probe_source_refs.py --mode refs --exclude "Core/Cloud"   # hole in closure
"""

import argparse
import glob
import io
import json
import os
import queue
import re
import subprocess
import sys
import threading
import time
from pathlib import Path

import psutil

if sys.platform == "win32":
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8", errors="replace")

WS = r"U:\Git\DO.Support-alcall"
DEFAULT_ROOTS = ["Core", "DeliveryNetwork", "DocumentOutput", "DocumentCapture"]
DEFAULT_ACTIVE = r"DocumentCapture\Cloud"

T0 = time.time()


def relws(p):
    try:
        return os.path.relpath(p, WS)
    except ValueError:
        return p


def log(msg):
    print(f"[{time.time() - T0:7.1f}s] {msg}", flush=True)


def find_host():
    base = os.path.expanduser(r"~\.vscode\extensions")
    exts = sorted(glob.glob(os.path.join(base, "ms-dynamics-smb.al-*")), reverse=True)
    for e in exts:
        for rel in (r"bin\Microsoft.Dynamics.Nav.EditorServices.Host.exe",
                    r"bin\win32\Microsoft.Dynamics.Nav.EditorServices.Host.exe"):
            p = os.path.join(e, rel)
            if os.path.exists(p):
                return p
    return None


# ---------------------------------------------------------------- manifests

def read_manifest(folder):
    try:
        with open(os.path.join(folder, "app.json"), encoding="utf-8-sig") as f:
            return json.load(f)
    except Exception:
        return None


def parse_version(v):
    try:
        return tuple(int(x) for x in str(v).split("."))
    except ValueError:
        return None  # e.g. "$(app_minimumVersion)"


def build_index(roots, exclude):
    """app id -> [folder]. Skips .alpackages and excluded folders."""
    index = {}
    for root in roots:
        for p in glob.glob(os.path.join(WS, root, "**", "app.json"), recursive=True):
            folder = os.path.normpath(os.path.dirname(p))
            r = relws(folder)
            if ".alpackages" in p or any(r.replace("\\", "/").startswith(x) for x in exclude):
                continue
            m = read_manifest(folder)
            if m and m.get("id"):
                index.setdefault(m["id"].lower(), []).append(folder)
    return index


APPLICATION_APP_ID = "c1335042-3002-4257-bf8a-75c898ccb1b8"
MAP_APPLICATION = False
EXTRA_CACHE = []


def project_references(folder, index, prefer):
    """Mirror of extension.js getProjectReferences/hasMatch:
    a folder satisfies a dependency when app ids match and the folder's
    version >= the dependency's version. Returns [(folder, manifest, dep)]."""
    m = read_manifest(folder)
    out, missing = [], []
    deps = list((m or {}).get("dependencies", []))
    if MAP_APPLICATION and (m or {}).get("application"):
        deps.append({"id": APPLICATION_APP_ID, "name": "Application", "publisher": "Microsoft",
                     "version": m["application"]})
    for dep in deps:
        dep_id = (dep.get("id") or dep.get("appId") or "").lower()
        hit = None
        for cand in index.get(dep_id, []):
            cm = read_manifest(cand)
            cv, dv = parse_version(cm.get("version")), parse_version(dep.get("version"))
            if cv is None or dv is None or cv < dv:
                continue
            # duplicates (Cloud vs OnPrem share ids): prefer the requested flavour
            if hit is None or prefer.lower() in cand.lower():
                hit = (cand, cm, dep)
        if hit:
            out.append(hit)
        else:
            missing.append(dep.get("name"))
    return out, missing


def closure(active, index, prefer):
    seen, order, refs, missing = set(), [], {}, {}

    def walk(f):
        key = f.lower()
        if key in seen:
            return
        seen.add(key)
        order.append(f)
        r, miss = project_references(f, index, prefer)
        refs[f], missing[f] = r, miss
        for cand, _, _ in r:
            walk(cand)

    walk(active)
    return order, refs, missing


# ---------------------------------------------------------------- LSP client

class Client:
    def __init__(self, exe):
        self.proc = subprocess.Popen([exe], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                     stderr=subprocess.DEVNULL, cwd=os.path.dirname(exe))
        self.ps = psutil.Process(self.proc.pid)
        self.q = queue.Queue()
        self.rid = 0
        self.pending = {}
        self.lock = threading.Lock()
        self.diags = {}
        self.messages = []          # window/showMessage + logMessage
        self.server_requests = []
        self.on_request = {}        # method -> callback(params)
        self.peak_rss = 0
        threading.Thread(target=self._reader, daemon=True).start()
        threading.Thread(target=self._mem, daemon=True).start()

    def _mem(self):
        while self.proc.poll() is None:
            try:
                rss = self.ps.memory_info().rss + sum(
                    c.memory_info().rss for c in self.ps.children(recursive=True))
                self.peak_rss = max(self.peak_rss, rss)
            except psutil.Error:
                pass
            time.sleep(0.5)

    def rss(self):
        try:
            return self.ps.memory_info().rss
        except psutil.Error:
            return 0

    def _reader(self):
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
            n = int(headers.get("content-length", 0))
            body = out.read(n)
            try:
                msg = json.loads(body.decode("utf-8"))
            except Exception:
                continue
            self._dispatch(msg)

    def _dispatch(self, msg):
        if "id" in msg and "method" not in msg:
            with self.lock:
                slot = self.pending.pop(msg["id"], None)
            if slot:
                slot.put(msg)
            return
        method = msg.get("method")
        params = msg.get("params") or {}
        if method == "textDocument/publishDiagnostics":
            self.diags[params.get("uri", "").lower()] = params.get("diagnostics", [])
        elif method in ("window/showMessage", "window/logMessage"):
            self.messages.append(params.get("message", ""))
        if "id" in msg:  # server -> client request
            self.server_requests.append(method)
            result = None
            if method == "workspace/configuration":
                result = [None] * len(params.get("items", []))
            self.send({"jsonrpc": "2.0", "id": msg["id"], "result": result})
            cb = self.on_request.get(method)
            if cb:
                threading.Thread(target=cb, args=(params,), daemon=True).start()

    def send(self, msg):
        data = json.dumps(msg).encode("utf-8")
        with self.lock:
            self.proc.stdin.write(b"Content-Length: %d\r\n\r\n" % len(data) + data)
            self.proc.stdin.flush()

    def notify(self, method, params):
        self.send({"jsonrpc": "2.0", "method": method, "params": params})

    def request(self, method, params, timeout=120):
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

    def stop(self):
        try:
            self.request("shutdown", None, timeout=10)
            self.notify("exit", None)
            self.proc.wait(timeout=10)
        except Exception:
            self.proc.kill()


# ---------------------------------------------------------------- settings

def settings(folder, *, active, closure_list=None, refs=None, parent=None, cache_paths=None):
    return {
        "workspacePath": folder,
        "alResourceConfigurationSettings": {
            "assemblyProbingPaths": ["./.netpackages"],
            "codeAnalyzers": [],
            "enableCodeAnalysis": False,
            "backgroundCodeAnalysis": "None",
            "packageCachePaths": (cache_paths or ["./.alpackages"]) + EXTRA_CACHE,
            "ruleSetPath": None,
            "enableCodeActions": True,
            "incrementalBuild": False,
            "outputAnalyzerStatistics": True,
            "enableExternalRulesets": True,
        },
        "setActiveWorkspace": active,
        "dependencyParentWorkspacePath": parent,
        "expectedProjectReferenceDefinitions": [
            {"appId": m["id"], "name": m["name"], "publisher": m["publisher"], "version": m["version"]}
            for _, m, _ in (refs or [])
        ],
        "activeWorkspaceClosure": closure_list if closure_list is not None else [],
    }


# ---------------------------------------------------------------- probes

PROBES = [
    # (label, file relative to WS, line-anchor regex, token, ops, expected owner app)
    ("sanity: local type CDC Document", r"DocumentCapture\Cloud\.dependencies\DC\Codeunit\CDCCaptureManagement.Codeunit.al",
     r"procedure CaptureFromXML", '"CDC Document"', ["hover", "definition"], "DocumentCapture"),
    ("permset CSC Basic (Core)", r"DocumentCapture\Cloud\Resources\CDCBasic.PermissionSet.al",
     r"IncludedPermissionSets", '"CSC Basic"', ["hover", "definition"], "Core"),
    ("permset CTS-CDN Basic (DeliveryNetwork)", r"DocumentCapture\Cloud\Resources\CDCBasic.PermissionSet.al",
     r"IncludedPermissionSets", '"CTS-CDN Basic"', ["hover", "definition"], "DeliveryNetwork"),
    ("permset CTS-CBF Read (missing app)", r"DocumentCapture\Cloud\Resources\CDCBasic.PermissionSet.al",
     r"IncludedPermissionSets", '"CTS-CBF Read"', ["hover", "definition"], "missing"),
    ("codeunit type CSC XML Document (Core)", r"DocumentCapture\Cloud\.dependencies\DC\Codeunit\CDCCaptureManagement.Codeunit.al",
     r"CSCXmlDoc: Codeunit \"CSC XML Document\"", '"CSC XML Document"', ["hover", "definition"], "Core"),
    ("method call FromSysXmlDocument (Core)", r"DocumentCapture\Cloud\.dependencies\DC\Codeunit\CDCCaptureManagement.Codeunit.al",
     r"CSCXmlDoc\.FromSysXmlDocument", "FromSysXmlDocument", ["hover", "definition", "references"], "Core"),
    ("base app Vend.Get (control, no symbols)", r"DocumentCapture\Cloud\.dependencies\DC\Codeunit\CDCAdvancedAppvlManagement.Codeunit.al",
     r"if Vend\.Get\(", "Get", ["hover", "definition"], "missing"),
    ("base app type Vendor (control)", r"DocumentCapture\Cloud\.dependencies\DC\Codeunit\CDCAdvancedAppvlManagement.Codeunit.al",
     r"Vend: Record Vendor;", "Vendor", ["hover", "definition"], "missing"),
    ("decl FromSysXmlDocument in Core (reverse refs)", r"Core\Cloud\Source\Legacy\Xml\XMLDocument.Codeunit.al",
     r"procedure FromSysXmlDocument", "FromSysXmlDocument", ["references"], "Core"),
]


def locate(path, anchor, token):
    with open(path, encoding="utf-8-sig") as f:
        lines = f.read().split("\n")
    rx = re.compile(anchor)
    for i, line in enumerate(lines):
        if rx.search(line):
            c = line.find(token)
            if c >= 0:
                return i, c + (1 if token.startswith('"') else 0) + 1
    raise ValueError(f"anchor {anchor!r} / {token!r} not found in {path}")


def summarize_locations(res):
    if not res:
        return []
    if isinstance(res, dict):
        res = [res]
    out = []
    for loc in res:
        uri = loc.get("uri") or loc.get("targetUri", "")
        rng = loc.get("range") or loc.get("targetSelectionRange") or {}
        out.append(f"{uri.split('DO.Support-alcall')[-1] if 'DO.Support-alcall' in uri else uri}:{rng.get('start', {}).get('line', '?') + 1 if rng else '?'}")
    return out


def hover_text(res):
    if not res:
        return None
    c = res.get("contents")
    if isinstance(c, dict):
        c = c.get("value")
    elif isinstance(c, list):
        c = " | ".join(x.get("value", "") if isinstance(x, dict) else str(x) for x in c)
    return (c or "").strip().replace("\n", " ")[:160] or None


def open_file(client, path, opened):
    if path.lower() in opened:
        return
    with open(path, encoding="utf-8-sig") as f:
        text = f.read()
    lang = "json" if path.endswith(".json") else "al"
    client.notify("textDocument/didOpen", {"textDocument": {
        "uri": Path(path).as_uri(), "languageId": lang, "version": 1, "text": text}})
    opened.add(path.lower())


def diag_summary(client, path):
    uri = Path(path).as_uri().lower()
    ds = None
    norm = lambda u: u.replace("%3a", ":").replace("%20", " ")
    for k, v in client.diags.items():
        if norm(k) == norm(uri):
            ds = v
    if ds is None:
        return {"published": False}
    errs = [d for d in ds if d.get("severity") == 1]
    codes = {}
    for d in errs:
        codes[str(d.get("code"))] = codes.get(str(d.get("code")), 0) + 1
    return {"published": True, "errors": len(errs), "total": len(ds), "by_code": codes,
            "samples": sorted({d.get("message", "")[:110] for d in errs})[:6]}


# ---------------------------------------------------------------- main

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--mode", choices=["baseline", "refs", "multiroot"], required=True,
                    help="multiroot = all closure folders as workspace folders, but closure=[active], no refs")
    ap.add_argument("--touch", action="store_true", help="send a no-op didChange to each probe file before settling")
    ap.add_argument("--active", default=DEFAULT_ACTIVE)
    ap.add_argument("--roots", nargs="*", default=DEFAULT_ROOTS)
    ap.add_argument("--exclude", nargs="*", default=[], help="folders (rel, / separated) to hide from the index")
    ap.add_argument("--prefer", default="Cloud", help="path fragment preferred when an app id has several folders")
    ap.add_argument("--no-loaded-callback", action="store_true",
                    help="send project-reference configs up front instead of on al/activeProjectLoaded")
    ap.add_argument("--skip-ref-configs", action="store_true",
                    help="never send the per-reference didChangeConfiguration (closure + refs only)")
    ap.add_argument("--load-timeout", type=int, default=900)
    ap.add_argument("--settle", type=int, default=20, help="seconds to wait for diagnostics after opening files")
    ap.add_argument("--probes", nargs="*", help="only run probes whose label contains one of these")
    ap.add_argument("--symbol-timeout", type=int, default=180)
    ap.add_argument("--out")
    ap.add_argument("--switch-to", help="after probes: activate this dependency project, re-probe, switch back, re-probe")
    ap.add_argument("--cache", nargs="*", default=[], help="extra packageCachePaths (absolute), like AL_LSP_PACKAGE_CACHE")
    ap.add_argument("--map-application", action="store_true",
                    help="treat app.json 'application' as a dependency on the Application app (not what VS Code does)")
    a = ap.parse_args()
    global MAP_APPLICATION
    MAP_APPLICATION = a.map_application
    global EXTRA_CACHE
    EXTRA_CACHE = a.cache

    exe = find_host()
    active = os.path.normpath(os.path.join(WS, a.active))
    report = {"mode": a.mode, "active": a.active, "exclude": a.exclude, "host": exe}

    if a.mode in ("refs", "multiroot"):
        index = build_index(a.roots, a.exclude)
        order, refs, missing = closure(active, index, a.prefer)
    else:
        order, refs, missing = [active], {active: []}, {}
    report["closure"] = [relws(f) for f in order]
    report["refs"] = {relws(k): [relws(c) for c, _, _ in v] for k, v in refs.items()}
    report["missing_deps"] = {relws(k): v for k, v in missing.items()}
    report["closure_al_files"] = sum(len(glob.glob(os.path.join(f, "**", "*.al"), recursive=True)) for f in order)
    log(f"mode={a.mode} closure={report['closure']} ({report['closure_al_files']} .al files)")
    for k, v in report["missing_deps"].items():
        log(f"  {k}: deps not in source: {v}")

    client = Client(exe)
    log(f"host pid={client.proc.pid}")
    folders = [{"uri": Path(f).as_uri(), "name": os.path.basename(f)} for f in order]

    parent_of = {}
    for p, rs in refs.items():
        for c, _, _ in rs:
            parent_of.setdefault(c, p)

    if a.mode == "multiroot":
        ws_order, order_for_cfg, refs = order, [active], {}
    else:
        ws_order, order_for_cfg = order, order

    def cfg_for(folder):
        if folder.lower() == active.lower():
            return settings(folder, active=True, closure_list=order_for_cfg, refs=refs.get(folder))
        return settings(folder, active=False, refs=refs.get(folder), parent=parent_of.get(folder))

    sent_refs = set()

    def send_ref_configs():
        if a.skip_ref_configs:
            return
        # loadProjectReferences: depth-first from the active project
        # extension.js dedups per (reference, parent) pair, and recursion is
        # guarded by a visited set of parents -- so a shared dep like Core gets
        # one config per project that references it.
        visited = set()

        def walk(f):
            if f in visited:
                return
            visited.add(f)
            for c, _, _ in refs.get(f, []):
                if (c, f) not in sent_refs:
                    sent_refs.add((c, f))
                    client.notify("workspace/didChangeConfiguration",
                                  {"settings": settings(c, active=False, refs=refs.get(c), parent=f)})
                walk(c)
        walk(active)
        log(f"  sent {len(sent_refs)} project-reference configs: {[(os.path.basename(os.path.dirname(c)) + chr(47) + os.path.basename(c), os.path.basename(os.path.dirname(p))) for c, p in sent_refs]}")

    loaded_evt = threading.Event()

    def on_loaded(params):
        log(f"  <- al/activeProjectLoaded {params}")
        report.setdefault("active_project_loaded_events", []).append(
            {"t": round(time.time() - T0, 1), "params": params})
        if a.mode == "refs" and not a.no_loaded_callback:
            send_ref_configs()
        loaded_evt.set()

    def on_config(params):
        for item in params.get("items", []):
            uri = item.get("scopeUri")
            folder = None
            if uri:
                p = os.path.normpath(uri.replace("file:///", "").replace("%3A", ":").replace("%3a", ":").replace("%20", " "))
                folder = next((f for f in order if f.lower() == p.lower()), None)
            folder = folder or active
            client.notify("workspace/didChangeConfiguration", {"settings": cfg_for(folder)})

    client.on_request["al/activeProjectLoaded"] = on_loaded
    client.on_request["workspace/configuration"] = on_config

    try:
        t = time.time()
        r = client.request("initialize", {
            "processId": os.getpid(), "rootUri": Path(active).as_uri(), "rootPath": active,
            "capabilities": {
                "workspace": {"configuration": True, "workspaceFolders": True,
                              "didChangeConfiguration": {"dynamicRegistration": True},
                              "symbol": {"dynamicRegistration": True}},
                "textDocument": {"hover": {"contentFormat": ["markdown", "plaintext"]},
                                 "definition": {"linkSupport": True},
                                 "references": {},
                                 "publishDiagnostics": {"relatedInformation": True},
                                 "synchronization": {"didSave": True}},
                "window": {"workDoneProgress": True},
            },
            "workspaceFolders": folders,
        })
        log(f"initialize: {'ok' if 'result' in r else r.get('error')}")
        client.notify("initialized", {})

        opened = set()
        # Like the wrapper: per-folder config + app.json + loadManifest
        for f in order:
            client.notify("workspace/didChangeConfiguration", {"settings": cfg_for(f)})
            open_file(client, os.path.join(f, "app.json"), opened)
            with open(os.path.join(f, "app.json"), encoding="utf-8-sig") as fh:
                raw = fh.read()
            client.request("al/loadManifest", {"projectFolder": f, "manifest": raw}, timeout=120)
        if a.mode == "refs" and a.no_loaded_callback:
            send_ref_configs()

        r = client.request("al/setActiveWorkspace", {
            "currentWorkspaceFolderPath": {"uri": Path(active).as_uri(), "name": os.path.basename(active), "index": 0},
            "settings": cfg_for(active)}, timeout=300)
        log(f"setActiveWorkspace: {json.dumps(r.get('result', r.get('error')))}")
        report["setActiveWorkspace"] = r.get("result", r.get("error"))

        # poll closure-loaded
        loaded = False
        polls = []
        while time.time() - t < a.load_timeout:
            r = client.request("al/hasProjectClosureLoadedRequest", {"workspacePath": active}, timeout=120)
            res = r.get("result")
            loaded = bool(res.get("loaded")) if isinstance(res, dict) else bool(res)
            polls.append(res)
            if loaded:
                break
            if len(polls) % 15 == 0:
                log(f"  still loading... rss={client.rss() / 2**20:.0f} MB")
            time.sleep(1)
        report["closure_loaded"] = loaded
        report["load_seconds"] = round(time.time() - t, 1)
        report["last_poll"] = polls[-1] if polls else None
        log(f"closure loaded={loaded} after {report['load_seconds']}s rss={client.rss() / 2**20:.0f} MB")
        loaded_evt.wait(timeout=30)

        # probes
        results = []
        probes = [p for p in PROBES if not a.probes or any(s in p[0] for s in a.probes)]
        for label, rel, anchor, token, ops, owner in probes:
            path = os.path.join(WS, rel)
            open_file(client, path, opened)
        time.sleep(3)
        for label, rel, anchor, token, ops, owner in probes:
            path = os.path.join(WS, rel)
            line, ch = locate(path, anchor, token)
            doc = {"uri": Path(path).as_uri()}
            pos = {"line": line, "character": ch}
            entry = {"label": label, "owner": owner, "pos": f"{line + 1}:{ch + 1}"}
            for op in ops:
                t1 = time.time()
                if op == "hover":
                    r = client.request("textDocument/hover", {"textDocument": doc, "position": pos}, timeout=60)
                    entry["hover"] = hover_text(r.get("result")) if "result" in r else f"ERR {r.get('error')}"
                elif op == "definition":
                    r = client.request("al/gotodefinition", {"textDocumentPositionParams": {"textDocument": doc, "position": pos}}, timeout=60)
                    entry["definition"] = summarize_locations(r.get("result")) if "result" in r else f"ERR {r.get('error')}"
                elif op == "references":
                    r = client.request("textDocument/references", {"textDocument": doc, "position": pos,
                                                                   "context": {"includeDeclaration": True}}, timeout=180)
                    locs = summarize_locations(r.get("result")) if "result" in r else None
                    entry["references"] = (f"{len(locs)} locs, apps: "
                                           f"{sorted({l.strip(chr(47)).split(chr(47))[0] for l in locs})}"
                                           if locs is not None else f"ERR {r.get('error')}")
                entry[f"{op}_ms"] = int((time.time() - t1) * 1000)
            results.append(entry)
            log(f"  {label}: " + json.dumps({k: v for k, v in entry.items() if k in ops}))
        report["probes"] = results

        if a.switch_to:
            dep = os.path.normpath(os.path.join(WS, a.switch_to))
            quick = [p for p in probes if "CSC Basic" in p[0] or "reverse refs" in p[0]]

            def activate(folder, clos, rfs):
                t1 = time.time()
                r = client.request("al/setActiveWorkspace", {
                    "currentWorkspaceFolderPath": {"uri": Path(folder).as_uri(), "name": os.path.basename(folder),
                                                   "index": next((i for i, f in enumerate(order) if f == folder), 0)},
                    "settings": settings(folder, active=True, closure_list=clos, refs=rfs)}, timeout=300)
                while time.time() - t1 < 300:
                    rr = client.request("al/hasProjectClosureLoadedRequest", {"workspacePath": folder}, timeout=60)
                    res = rr.get("result")
                    if (res.get("loaded") if isinstance(res, dict) else res):
                        break
                    time.sleep(0.5)
                return r.get("result", r.get("error")), round(time.time() - t1, 1)

            def quick_probe(tag):
                out = {}
                for label, relp, anchor_, token, ops, owner in quick:
                    path = os.path.join(WS, relp)
                    line, ch = locate(path, anchor_, token)
                    doc, pos = {"uri": Path(path).as_uri()}, {"line": line, "character": ch}
                    if "references" in ops:
                        r = client.request("textDocument/references", {"textDocument": doc, "position": pos,
                                           "context": {"includeDeclaration": True}}, timeout=120)
                        out[label] = len(r.get("result") or [])
                    else:
                        r = client.request("textDocument/hover", {"textDocument": doc, "position": pos}, timeout=60)
                        out[label] = hover_text(r.get("result"))
                log(f"  [{tag}] {out}")
                return out

            dep_order, dep_refs, _ = closure(dep, build_index(a.roots, a.exclude), a.prefer)
            res, secs = activate(dep, dep_order, dep_refs.get(dep))
            log(f"  switch -> {a.switch_to}: {res} in {secs}s (closure {[relws(f) for f in dep_order]})")
            report["switch_to_dep"] = {"result": res, "seconds": secs, "probe": quick_probe("dep active")}
            res, secs = activate(active, order, refs.get(active))
            log(f"  switch back -> {a.active}: {res} in {secs}s")
            report["switch_back"] = {"result": res, "seconds": secs, "probe": quick_probe("back")}

        if a.touch:
            for p in {pp[1] for pp in probes}:
                path = os.path.join(WS, p)
                with open(path, encoding="utf-8-sig") as fh:
                    txt = fh.read()
                client.notify("textDocument/didChange", {"textDocument": {"uri": Path(path).as_uri(), "version": 2},
                                                          "contentChanges": [{"text": txt}]})
        series = []
        client.ps.cpu_percent(None)
        for i in range(a.settle):
            time.sleep(1)
            if i % 5 == 4:
                series.append((i + 1, len(client.diags), sum(1 for v in client.diags.values() if v),
                               round(client.ps.cpu_percent(None))))
        report["diag_series(sec,uris,uris_with_diags)"] = series
        report["diag_uris"] = sorted(client.diags)
        log(f"  diag series: {series}")
        report["diagnostics"] = {os.path.relpath(os.path.join(WS, rel), WS): diag_summary(client, os.path.join(WS, rel))
                                 for rel in sorted({p[1] for p in probes})}
        for k, v in report["diagnostics"].items():
            log(f"  diag {k}: {json.dumps({x: v.get(x) for x in ('errors', 'by_code')})}")

        for method, params in (("workspace/symbol", {"query": "CSC XML Document"}),
                               ("al/symbolSearch", {"query": "CSC XML Document"})):
            t1 = time.time()
            r = client.request(method, params, timeout=a.symbol_timeout)
            res = r.get("result")
            if isinstance(res, dict):
                res = res.get("symbols")
            report[method] = ([(x.get("name"), summarize_locations(x.get("location"))) for x in (res or [])[:8]]
                              if "result" in r else f"ERR {r.get('error')}")
            log(f"  {method} ({time.time() - t1:.1f}s): {report[method]}")
        report["peak_rss_mb"] = round(client.peak_rss / 2**20)
        report["server_requests"] = sorted(set(client.server_requests))
        report["messages"] = client.messages[-30:]
        log(f"peak rss={report['peak_rss_mb']} MB; server requests={report['server_requests']}")
    finally:
        client.stop()
        if a.out:
            with open(a.out, "w", encoding="utf-8") as f:
                json.dump(report, f, indent=2)
            log(f"wrote {a.out}")


if __name__ == "__main__":
    main()
