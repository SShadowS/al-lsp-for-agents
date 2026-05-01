# Phase 1.7 — Verbose Log Analysis

Captured AL Editor Services Host's verbose log against the minimized
fixture (Phase 1.3) by setting `al.editorServicesLogLevel: "Verbose"`
in `Cloud/.vscode/settings.json`. Goal: confirm the FS-scan-vs-didOpen
race hypothesis from Phase 1.5 (decompilation) with runtime evidence.

## Setup

- Fixture: `harness/work/Core` in the 5/5-reproducing minimal state
  (11 Continia AL files in `Cloud/AL/Activation/`,
  `Cloud/.alpackages` present, no `.dependencies`, no self.app at
  Cloud root).
- Workspace root: `Cloud/` (single-root, `openSubFolder = "Cloud"`).
- Cell: `cell-bisect` with only `ms-dynamics-smb.al` extension
  installed.
- Setting: `Cloud/.vscode/settings.json` with
  `"al.editorServicesLogLevel": "Verbose"` and
  `"al.enableCodeAnalysis": false`.
- Run: `npm run test:cell -- cell-bisect --record`. Snapshot
  confirmed AL0264 + AL0197 fired.
- Log: `harness/out/extensions/cell-bisect/ms-dynamics-smb.al-18.0.2190758/bin/win32/EditorServices.log`,
  30,962 lines after one run.

## Limit of this trace

The Editor Services Host verbose log captures protocol method names
and timing but does NOT include JSON-RPC params (URIs, document
content, etc.). To get a true wire trace, a stdio-proxy in front of
`Microsoft.Dynamics.Nav.EditorServices.Host.exe` would be needed —
not built yet. What we have below is "AL LS's own activity log,"
which is enough to corroborate the multi-pass parsing pattern but
does not by itself prove the file-path identity of the duplicate
SyntaxTrees.

## Smoking gun: multi-pass binding of the same source

The verbose log records every AST node binding pass. For
`AppExperienceImpl.InsertIntoBuffer(...)` — a method called from
`AppExperience.Codeunit.al` — the same statement is bound from
**three** distinct worker threads in rapid succession:

```
2026-05-01T23:21:31.5724751 [36/15] Binding Statement: AppExperienceImpl.InsertIntoBuffer(...)
2026-05-01T23:21:31.5788764 [36/15] Binding Statement: AppExperienceImpl.InsertIntoBuffer(...)
... [36/15] continues binding the rest of the codeunit ...
2026-05-01T23:21:31.5837888 [36/15] Binding Statement: TemporaryAppBuffer.Copy(...)

2026-05-01T23:21:31.6116741 [/28]  Binding Statement: AppExperienceImpl.InsertIntoBuffer(...)
2026-05-01T23:21:31.6219760 [/28]  Binding Statement: AppExperienceImpl.InsertIntoBuffer(...)
... [/28] continues ...

2026-05-01T23:21:31.6987834 [47/20] Binding Statement: AppExperienceImpl.InsertIntoBuffer(...)
2026-05-01T23:21:31.6992572 [47/20] Binding Statement: AppExperienceImpl.InsertIntoBuffer(...)
2026-05-01T23:21:31.6999514 [47/20] Binding Statement: ...
... [47/20] continues with all the same statements in the same order ...
2026-05-01T23:21:31.7037888 [47/20] Binding Statement: TemporaryAppBuffer.Copy(...)
```

Three threads each parse and bind the same set of statements with
the same identity. This is the runtime correlate of the SymbolMap
state described in the decompilation analysis: multiple
SourceCodeunitTypeSymbol instances exist for Codeunit 6225384
("CSC App Experience"), each created from a different SyntaxTree,
each undergoing its own binding pipeline.

The binding-pass thread tags appear inconsistent (`[36/15]`,
`[/28]`, `[47/20]`) — the format is `[workerId/connectionId]` but
the middle field varies. This is an artifact of AL LS's task
scheduler; the pattern that matters is "the same source gets bound
N times from N different workers," consistent with N
SourceCodeunitTypeSymbols feeding N parallel binding pipelines.

## LSP request sequence

Filtered for top-level requests/notifications:

```
T+0.000  startup
T+0.245  al/hasProjectClosureLoadedRequest
T+0.366  textDocument/didOpen          (#1, presumably app.json)
T+0.367  al/loadManifest               (×2)
T+0.370  al/setActiveWorkspace         (took 0.865 s — workspace scan)
T+1.239  workspace/didChangeConfiguration
T+1.247  workspace/didChangeWatchedFiles
T+1.250  al/loadManifest
T+1.252  al/didChangeActiveDocument
T+1.252  textDocument/semanticTokens/full
T+1.254  al/hasProjectClosureLoadedRequest
T+1.254  textDocument/didOpen          (#2, presumably AppExperience.Codeunit.al)
T+1.255  al/didChangeActiveDocument    (×2)
... binding work begins around T+6.0 ...
T+6.155  multi-pass binding of CSC App Experience source
```

Critical timing: `al/setActiveWorkspace` runs for 865 ms before the
second `didOpen`. This is the workspace scan from Phase 1.5's
hypothesis — AL LS walks the project files via filesystem and
constructs SyntaxTrees during this window. The second `didOpen`
arrives AFTER the scan is complete, almost certainly causing AL LS
to add a SECOND SyntaxTree for the same path the scan already
processed.

This matches the Phase 1.5 mechanism exactly:

1. `al/setActiveWorkspace` triggers FS scan
2. FS scan creates SyntaxTree A for `AppExperience.Codeunit.al`
3. SyntaxTree A goes through DeclarationTreeBuilder, produces
   SingleObjectDeclaration for Codeunit 6225384
4. `textDocument/didOpen` arrives for `AppExperience.Codeunit.al`
5. AL LS treats it as a new tree → SyntaxTree B
6. SyntaxTree B goes through DeclarationTreeBuilder, produces
   ANOTHER SingleObjectDeclaration with the same identity
7. MergedNamespaceDeclaration.MakeChildren keeps both
   SingleObjectDeclarations under one MergedObjectDeclaration
8. SourceNamespaceSymbol.BuildObjectSymbolsFromDeclaration iterates
   both SyntaxReferences, creates two SourceCodeunitTypeSymbols
9. SymbolMap now has two ContainerSymbols for Codeunit 6225384
10. Both undergo binding (the multi-pass we observe in the log)
11. SourceModuleSymbol.CheckForDuplicateIds sees two entries with
    same id, reports AL0264 / AL0197

## Explains "empty stub bodies don't trigger"

In Phase 1.3 we observed that replacing the 11 file bodies with
empty stubs drops repro to 0/3. The verbose log makes this fit:
empty-body files parse and bind almost instantly. The FS scan
completes *before* the `didOpen` for `AppExperience.Codeunit.al`
arrives only by milliseconds, OR the scan finishes before
`didOpen` even reaches the LS. Either way, by the time `didOpen`
hits, AL LS may detect the file is already known (perhaps via a
content-hash or version check that succeeds for short files but not
for longer ones), or the scan vs didOpen ordering is reversed and
the scan doesn't add a duplicate tree.

This is consistent with the racy small-fixture / deterministic
large-fixture pattern (Phase 1.3): more code = longer FS-scan parse
time = wider window for `didOpen` to arrive mid-scan = higher repro
rate.

## What we have NOT yet proven

- The exact contents of each `textDocument/didOpen` (which file,
  with what version). Verbose log shows method names and timing
  only; URIs would require a JSON-RPC stdio proxy in front of
  EditorServices.Host.exe.
- That AL LS truly creates two SyntaxTrees with the same path
  (only inferred from "two SourceCodeunitTypeSymbols exist" via the
  multi-pass binding pattern).
- Whether the `al.trace.server` standard VS Code trace setting can
  yield the wire trace with less infrastructure work — tested with
  `al.trace.server`, `AL.trace.server`, and `[al].trace.server`,
  none produced JSON-RPC output to console or the AL output channel
  in headless mode.

## Next investigation step (if MS asks for confirmation)

Build a small stdio-proxy executable that:
1. Spawns `Microsoft.Dynamics.Nav.EditorServices.Host.exe.real`
   with all original args
2. Forwards stdin from VS Code → host, logging each frame
3. Forwards host stdout → VS Code, logging each frame
4. Writes timestamped frames to a file under
   `harness/work/traces/<run-id>.ndjson`

Then rename the per-cell extensions dir's
`Microsoft.Dynamics.Nav.EditorServices.Host.exe` to `.real` and
drop in the proxy under the original name. AL extension launches
the proxy, proxy spawns the real host. Trace captures every frame.

Estimated effort: ~2 hours. Not built yet. The decompilation
analysis (Phase 1.5) plus this verbose log evidence is enough to
file the upstream bug; a wire trace would only be required if MS
engineers can't reproduce internally and need our exact frame
sequence.

## Status

- Verbose-log evidence corroborates Phase 1.5's mechanism
  hypothesis: multi-pass binding of the same source code, indicative
  of multiple SyntaxTrees for the same file in the Compilation.
- Direct frame-level confirmation deferred pending decision on
  building the stdio-proxy.
- Combined with Phase 1.3 (minimized repro) and Phase 1.5
  (decompilation), the evidence is sufficient to file the upstream
  Microsoft bug.
