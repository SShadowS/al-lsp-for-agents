# Phase 1.8 — stdio-proxy wire trace

This phase built a single-purpose stdio proxy that intercepts JSON-RPC
between VS Code and `Microsoft.Dynamics.Nav.EditorServices.Host.exe`.
Goal: capture frame-level traffic so we can confirm the FS-scan-vs-
didOpen race directly, beyond what 04-wire-trace-analysis.md could
infer from the AL verbose log.

The trace produced an unexpected, sharper finding: **the duplicate
SyntaxTree comes from a path-casing mismatch**, not just from a
generic FS-vs-didOpen race. Details below.

**Filed upstream as <https://github.com/microsoft/AL/issues/8249>**
(related to the closed `microsoft/AL#6077`, which fixed the
file-rename variant of the same symptom in BC 22.0).

## What was built

`al-language-server-go/cmd/lsp-trace-proxy/main.go` — Go binary that:

1. Reads its argv and forwards verbatim to a sibling
   `Microsoft.Dynamics.Nav.EditorServices.Host.exe.real`.
2. Pipes stdin (VS Code -> AL LS) and stdout (AL LS -> VS Code) byte
   for byte. Frames are forwarded *without* parse-and-reserialize, so
   nothing is normalized away.
3. Side effect: parses each LSP frame and appends one NDJSON record
   per frame to `$AL_LSP_TRACE_FILE`, with `dir: in|out`, `method`,
   `id`, `params|result|error`, length, and a UTC RFC3339Nano
   timestamp. Plus a one-record `banner` at startup with argv + pid.

Smoke test under `cmd/lsp-trace-proxy/smoketest/smoke.go` builds an
echo-back fake "real" and verifies that 2 frames in produce 2 frames
out byte-identical, with the expected trace shape.

The .NET apphost stub (`Host.exe`) embeds the path to its companion
`.dll` at publish time (verified via `strings`), so renaming
`Host.exe` to `Host.exe.real` is safe — the stub still finds
`Microsoft.Dynamics.Nav.EditorServices.Host.dll` next to itself.

## How the harness installs it

`runOneCell.ts` swap is opt-in via `HARNESS_BISECT_TRACE=1`. After
`code --install-extension` finishes:

1. Find the AL extension under `harness/out/extensions/<cell>/
   ms-dynamics-smb.al-*` and locate its `bin/win32/Host.exe`.
2. If `Host.exe.real` doesn't exist yet, rename the original
   `Host.exe` to `.real`. (Idempotent — second run just overwrites
   the proxy.)
3. Copy our `lsp-trace-proxy.exe` into `Host.exe`.
4. Set `AL_LSP_TRACE_FILE=harness/out/traces/<cell>.ndjson` in
   `extensionTestsEnv`.

When `HARNESS_BISECT_TRACE` is unset, the install is unchanged. Other
cells/baselines are not affected.

## Run that produced the trace

```
HARNESS_BISECT_TRACE=1 npm run test:cell -- cell-bisect
```

Cell config (defaults):
- fixture: `harness/work/Core` (the 5/5-deterministic minimal-repro
  state — 11 specific files under `Cloud/AL/Activation/`).
- exts: `control` (ms-dynamics-smb.al only, no wrapper, no
  test-runner). Confirms the bug isn't from any third-party.
- openFile: `Cloud/Al/Activation/AppExperience.Codeunit.al` (note
  the casing — see below).

Trace: `harness/out/traces/cell-bisect.ndjson`, 103 records.

The captured run reproduced AL0264/AL0197 — the test compared the
captured snapshot against the existing baseline and reported the
expected duplicate-decl errors as part of the diff. Proxy did not
suppress the bug.

The trace is a partial capture: 103 frames covering ~3.5 seconds
from server spawn through the early codeLens responses. Diagnostics
publication continued for ~40 seconds after that, but the proxy
stopped writing because vscode disconnected its end of stdout while
the real host kept producing output (proxy's `out` goroutine errored
and returned, then `cmd.Wait` blocked on the still-running real
process). For Phase 1.8's purpose — confirming what happens before
diagnostics are published — the early window is exactly what we
needed.

## Findings

### Sequence of LSP frames (filtered)

```
22:39:22.103  banner             (proxy starts)
22:39:22.316  in  initialize
22:39:22.398  in  initialized
22:39:22.448  in  workspace/didChangeConfiguration
22:39:22.448  in  al/setActiveWorkspace      #1   id=28
22:39:22.481  in  textDocument/didOpen       (AppExperience.Codeunit.al)
22:39:22.513  in  al/didChangeActiveDocument id=29
22:39:22.826  in  workspace/didChangeWatchedFiles
22:39:23.587  out al/manifestMissing
22:39:23.587  out textDocument/publishDiagnostics  (app.json, 0 diags)
22:39:24.085  in  al/setActiveWorkspace      #2   id=39
22:39:24.553  out textDocument/publishDiagnostics  (app.json, 0 diags)
25.5  ...   trace cuts off here
```

Method counters (full trace):
- inbound (VS Code -> AL LS): `initialize`, `initialized`,
  `workspace/didChangeConfiguration`, `al/setActiveWorkspace` x2,
  `textDocument/didOpen` x1, `al/didChangeActiveDocument` x1,
  `workspace/didChangeWatchedFiles` x1, plus document-feature
  requests (semanticTokens, documentSymbol, codeLens, codeAction,
  documentHighlight, foldingRange, codeLens/resolve).
- outbound (AL LS -> VS Code): `al/manifestMissing` x1,
  `al/refreshExplorerObjects` x3, `textDocument/publishDiagnostics`
  x3 (all on `app.json` with 0 diags). Many request responses by id.

### didOpen happened exactly once — and it's not the duplicate source

Hypothesis from earlier phases was "two `textDocument/didOpen` for
the same path." The trace falsifies that: there's exactly **one**
`textDocument/didOpen` for `AppExperience.Codeunit.al`, and the
URI is well-formed. So duplicate trees do not come from the editor
side.

### `al/setActiveWorkspace` doesn't carry a file list

Both `al/setActiveWorkspace` calls send only:
- the workspace folder path (`fixture` root)
- AL settings (analyzers, package-cache paths, etc.)
- `activeWorkspaceClosure: [<workspace root>]`

There's no manifest, no file enumeration, no source content. Whatever
files MS AL LS ends up parsing during this call, it discovers them
itself by walking the workspace folder.

### The duplicate tree comes from a path-casing mismatch

The cell-bisect cell opens
`Cloud/Al/Activation/AppExperience.Codeunit.al` (lowercase `Al`).
That's how it's spelled in `cells/cell-bisect.ts`. On disk, the
directory is `Cloud/AL/Activation/` (uppercase `AL`).

Windows is case-insensitive, so VS Code resolves the lowercase path
to the actual file. The didOpen URI it sends to AL LS preserves the
caller's casing:

```
file:///c%3A/.../fixture/Cloud/Al/Activation/AppExperience.Codeunit.al
```

When `al/setActiveWorkspace` triggers MS AL LS's own filesystem walk,
the LS reads directory entries from the Win32 API and sees the true
on-disk casing. Files it discovers carry the **uppercase** `AL`
spelling.

The baseline snapshot at `harness/baseline/cell-bisect.json` — which
was recorded in earlier proxy-less runs and matches the on-disk
behavior — confirms this split:

| URI casing       | diag count | example                                                |
|------------------|-----------:|--------------------------------------------------------|
| `Cloud/Al/...`   | 13         | `Cloud/Al/Activation/AppExperience.Codeunit.al`        |
| `Cloud/AL/...`   | 169        | `Cloud/AL/Activation/AppExperience.Codeunit.al` (1) and 10 other files |
| other (deps etc) | 161        | `Cloud/.dependencies/CC/Codeunit/...`                  |

Both AL0264 and AL0197 fire at line 0 of
`Cloud/Al/Activation/AppExperience.Codeunit.al` (the lowercase URI,
i.e. the didOpen tree). The same file also appears under
`Cloud/AL/Activation/AppExperience.Codeunit.al` with the AL0185
errors that the rest of the FS-scan-loaded files have.

So MS AL LS is treating the two URIs as **different files**, even
though Windows considers them the same path. The DeclarationTree-
Builder produces one `SingleObjectDeclaration` per URI for Codeunit
6225384, and `MergedNamespaceDeclaration.MakeChildren` merges them
under one `MergedObjectDeclaration` with two `SyntaxReferences`. From
there the chain in 02-decompilation-notes.md takes over:
`BuildObjectSymbolsFromDeclaration` creates two
`SourceCodeunitTypeSymbol` entries, the SymbolMap has duplicates,
and `CheckForDuplicateIds` fires AL0264/AL0197.

### Race window

The two-tree state exists **before** any diagnostics are published.
The FS-scan side enters during/just after `al/setActiveWorkspace #1`
(22.448s); the didOpen side enters at 22.481s — only 33 ms later.
That tiny gap is enough for both trees to be present in the same
Compilation by the time symbol-binding runs and the duplicate-id
check fires.

The second `al/setActiveWorkspace` at 24.085 (1.6 s later) likely
reaches the same divergent state. Beyond that the trace cuts off
before MS AL LS publishes the AL0264/AL0197 frame to VS Code, but
the harness baseline (recorded from clean unproxied runs) shows the
diagnostics that eventually arrive.

## Implications

1. **The bug is path-canonicalisation, not "two SyntaxTrees" per se.**
   MS AL LS doesn't case-fold path strings on Windows when keying
   `SyntaxTree` / `SingleObjectDeclaration` instances. Two paths
   that the OS treats as identical end up as separate logical
   entities inside the LS.

2. **Real-world repro path.** VS Code's "go to file" / workspace-
   symbol-by-name flows can produce URIs whose casing differs from
   the on-disk filename (cached entry, user-typed path, search
   result with normalized capitalisation, multi-root workspace
   merge). Any time the editor sends a `textDocument/didOpen` whose
   URI casing differs from the FS-walked casing, AL LS's compiler
   will see two declarations of every object in that file and fire
   AL0197/AL0264 against the project itself.

3. **Why issue #17 looked like an interaction with AL Test Runner /
   our wrapper.** Both extensions can issue their own `didOpen`
   notifications (AL Test Runner: when discovering tests; our
   wrapper: forwarding to the inner LS) using URIs that may not
   match the FS casing exactly. Triggering this case-sensitive
   mismatch through one of those paths would surface as the bug
   appearing only when extension X is installed.

4. **Wrapper hardening, not workaround.** Per the project's stance
   on not silently masking upstream bugs, we should leave the bug
   visible to MS for repro/fix. But the wrapper can defensively
   normalise URIs it forwards (canonical Windows long-path with
   on-disk casing via `GetFinalPathNameByHandle`) so that issue
   #17 stops firing through *our* code path while the upstream fix
   is in flight.

## Repro recipe (for the MS bug report)

Minimum demonstrably-bad input:

1. AL extension 18.0.2190758 (or 18.0.2293710) on Windows.
2. Workspace: any AL project. Open one .al file via a URI whose
   path component differs in casing from the on-disk path
   (uppercase vs lowercase directory or filename).
3. `al/setActiveWorkspace` arrives, then `textDocument/didOpen`
   with the case-divergent URI.

Symptom: AL0197/AL0264 fire on the file claiming it's "already
declared by extension <self>". Closing and reopening the file with
its true on-disk casing makes the errors disappear.

Synthetic fixture inside the harness (`harness/work/Core` plus the
11-file `Cloud/AL/Activation/` subset, opened via
`Cloud/Al/Activation/AppExperience.Codeunit.al`) reproduces 5/5.

## What was not done

- Did not extend the proxy with shutdown handling (drain outPipe and
  kill the real process when peer disconnects). The trace cut-off is
  cosmetic — the early window is what we needed for Phase 1.8.
- Did not fix the EBUSY-on-rmdir cleanup race. Same reason: the
  fixture is in a temp dir that the OS will reap, and re-running the
  cell first-thing recreates state.
- Did not write a wrapper-side URI canonicaliser. That is a Layer 1
  concern, separate from the investigation.
