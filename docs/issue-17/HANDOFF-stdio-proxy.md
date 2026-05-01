# Handoff — build the stdio-proxy wire tracer

You're picking up issue #17 investigation. Phases 1.3, 1.5, and 1.7
are complete and committed. Phase 1.8 (this handoff) is to build a
stdio proxy that intercepts JSON-RPC traffic between VS Code and the
Microsoft AL Language Server, producing a frame-level trace that
confirms the FS-scan-vs-didOpen race directly.

## Bug being investigated

False-positive AL0264 / AL0197 errors:

```
AL0197  An application object of type 'Codeunit' with name
        'CSC App Experience' is already declared by the extension
        'Continia Core by Continia Software (28.0.0.0)'

AL0264  An application object of type 'Codeunit' with ID '6225384'
        is already declared by the extension 'Continia Core by
        Continia Software (28.0.0.0)'
```

Reported in https://github.com/SShadowS/al-lsp-for-agents/issues/17.
The "extension X" in the message is the open project itself —
i.e. MS AL LS thinks the same extension is registered twice.

## What is committed

- `harness/` — VS Code matrix harness, reproduces the bug
  deterministically against a real Continia BC project.
  - `cell-real-control` (MS AL only, no wrapper) — 5/5 fires.
  - `cell-bisect` (MS AL only, env-driven fixture path) — used
    for shrinking experiments.
- `harness/work/Core/` — scratch copy of `u:\Git\DO.Support-UISimply\
  Core/`, currently in the "5/5 deterministic minimal repro" state:
  - `Cloud/AL/Activation/` contains 11 specific real Continia files
    (the bisected-down minimum).
  - `Cloud/.alpackages/` populated with MS + Continia System
    Application symbols.
  - Other Cloud subdirs and Test/ folder removed.
- `docs/issue-17/00-investigation-log.md` — chronological log of
  every experiment.
- `docs/issue-17/02-decompilation-notes.md` — Phase 1.5
  decompilation of `Microsoft.Dynamics.Nav.CodeAnalysis.dll` and
  `Workspaces.dll`. Pinpoints `BuildObjectSymbolsFromDeclaration` in
  `SourceModuleSymbol.cs:454` as the bug site (creates one symbol
  per SyntaxReference, not per merged identity).
- `docs/issue-17/03-minimized-repro.md` — Phase 1.3 bisection trail
  and reproduction recipe.
- `docs/issue-17/04-wire-trace-analysis.md` — Phase 1.7 verbose-log
  analysis. Multi-pass binding pattern + `al/setActiveWorkspace`
  timing corroborate the race. Notes that JSON-RPC params are NOT
  in the AL verbose log — that's why we need the proxy.

Recent commits:

```
0d7f586 issue-17: verbose log corroborates the FS-scan-vs-didOpen race
7392be5 issue-17: decompile MS AL LS to find AL0264 root cause
aa9257d issue-17: minimal reproducer bisected to 11 files
6734ac6 harness: reproduce issue #17 against real BC project
```

## Working hypothesis (to confirm with the proxy)

The MS AL LS pipeline:

1. `al/setActiveWorkspace` triggers a workspace filesystem scan
   (~865 ms in our trace). Each `.al` file gets parsed → SyntaxTree.
2. `textDocument/didOpen` arrives for `AppExperience.Codeunit.al`
   AFTER the scan completes. AL LS adds another SyntaxTree for the
   editor buffer.
3. The Compilation now has TWO SyntaxTrees pointing at the same
   file path. The DeclarationTreeBuilder produces two
   SingleObjectDeclarations with identical identity for Codeunit
   6225384.
4. `MergedNamespaceDeclaration.MakeChildren` keeps both under one
   MergedObjectDeclaration.
5. `SourceNamespaceSymbol.BuildObjectSymbolsFromDeclaration` —
   buggy site — iterates `declaration.SyntaxReferences` and creates
   a separate `SourceCodeunitTypeSymbol` per reference. The
   SymbolMap ends up with two ContainerSymbols for one logical
   codeunit.
6. `SourceModuleSymbol.CheckForDuplicateIds` correctly observes the
   duplicate and fires AL0264 / AL0197.

The proxy should let us prove (1)–(2) directly: capture the
sequence of LSP frames for `AppExperience.Codeunit.al` and confirm
that AL LS receives `textDocument/didOpen` for it AFTER it has
already loaded the same content via FS scan (look for any AL custom
notification or workspace event that delivers FS contents prior to
the editor's didOpen).

## What to build

A stdio-proxy executable (Go preferred, since we have a Go
toolchain in the repo) that:

1. Accepts the same args as `Microsoft.Dynamics.Nav.EditorServices.
   Host.exe`.
2. Spawns the real host (renamed to `.exe.real`) with the same args.
3. Pipes stdin from VS Code → real host, stdout from real host →
   VS Code, verbatim.
4. As a side effect, parses each LSP frame in both directions and
   appends an NDJSON record to a log file:
   ```json
   {"ts":"2026-05-01T23:21:25.383Z","dir":"in","method":"textDocument/didOpen","id":null,"params":{...}}
   {"ts":"2026-05-01T23:21:25.388Z","dir":"out","method":null,"id":42,"result":{...}}
   ```
   `dir:"in"` = VS Code → AL LS, `dir:"out"` = AL LS → VS Code.
5. Log file path from env var `AL_LSP_TRACE_FILE` (default to a
   temp path). Append-only so multiple host invocations during one
   harness run all land in one file.

Suggested location: `al-language-server-go/cmd/lsp-trace-proxy/`
(new sibling to the wrapper). Build into a single Windows .exe.

## How to install the proxy in the harness

The AL extension launches `Microsoft.Dynamics.Nav.EditorServices.
Host.exe` from its own `bin/win32/` directory. The harness installs
the AL extension into a per-cell extensions dir at
`harness/out/extensions/<cell>/ms-dynamics-smb.al-18.0.2190758/bin/
win32/`. To intercept:

1. After the cell installs the AL extension (after
   `code --install-extension` completes, before `runTests`), rename
   `Microsoft.Dynamics.Nav.EditorServices.Host.exe` to `.real`.
2. Drop the proxy binary in as
   `Microsoft.Dynamics.Nav.EditorServices.Host.exe`.
3. Set `AL_LSP_TRACE_FILE` in `extensionTestsEnv` to a known path
   like `harness/out/traces/<cell-name>.ndjson`.
4. Run the cell. Trace file accumulates frames.

Implementation hint: do this in `runOneCell.ts` only when an env
var like `HARNESS_BISECT_TRACE=1` is set. Don't enable by default —
the proxy adds latency and we don't want to taint other cells.

## Verifying the proxy

1. Run cell-bisect with the proxy, fixture in current state
   (`harness/work/Core` with the 11-file Cloud/AL/Activation/).
2. Confirm AL0264/AL0197 still fire in the resulting baseline (the
   proxy must not change behavior — only observe).
3. In the trace file, find every frame whose method or params
   reference `AppExperience.Codeunit.al`. Count textDocument/didOpen
   notifications for that file. Look for AL-custom notifications
   like `al/loadManifest`, `al/setActiveWorkspace`, or anything
   that might deliver source content from the FS-scanner.

## Verifying the hypothesis

Goal: in the trace, identify the moment(s) the LS could have learned
about `AppExperience.Codeunit.al`'s content, and whether any of
those is followed by a `textDocument/didOpen` for the same path.

Specifically, look for:

- A single `textDocument/didOpen` for the path, OR
- Multiple `textDocument/didOpen` for the same path (this would
  confirm the duplicate trees come from the editor side), OR
- One `textDocument/didOpen` plus an AL-internal mechanism (e.g.
  `al/setActiveWorkspace` returning a manifest that lists files
  the LS then reads from disk on its own) — this would confirm
  the FS-scan side.

If the answer is "the LS reads files from disk during
setActiveWorkspace, and didOpen is later," that's the FS-scan path
contributing the first SyntaxTree.

## Don't break

- Existing harness baselines for the 5 default cells, the 4
  synthetic-fixture cells, and the 4 real-fixture cells. Trace
  mode is opt-in.
- The work dir state at `harness/work/Core/` — it's the
  5/5-reproducing minimum. Don't bisect further or you may lose
  the deterministic property.
- The investigation logs in `docs/issue-17/`. Append a new file
  `06-stdio-proxy.md` documenting the proxy design + trace
  findings, don't edit existing ones.

## Don't waste time on

- Building the proxy as a generic LSP debugger. Single-purpose
  trace tool is fine. Future investigations can extend it.
- VS Code's `<langId>.trace.server` setting — already tried, AL
  extension doesn't respect it.
- Standardizing the trace format beyond NDJSON. We just need to
  read it.
- Anything outside `docs/issue-17/`, `harness/`, and the new
  `al-language-server-go/cmd/lsp-trace-proxy/` dir.

## Environment notes

- Repo: `u:\Git\claude-code-lsps`
- Shell: Git Bash on Windows. Use absolute paths in `rm` (Safety
  Net blocks `rm -rf` outside CWD; `cd` doesn't persist between
  Bash tool calls).
- `MSYS_NO_PATHCONV=1` is needed when invoking Windows tools that
  take `/flag:value` args, otherwise MSYS rewrites `/foo` paths
  into Unix-style paths.
- AL CLI compiler at
  `c:\Users\SShadowS\.vscode\extensions\ms-dynamics-smb.al-18.0.2293710\bin\win32\alc.exe`.
- ilspycmd at `c:\Users\SShadowS\.dotnet\tools\ilspycmd.exe`,
  decompiled DLLs at `harness/work/decompiled/{CodeAnalysis,
  EditorServices, Workspaces}/`.
- Original DO.Support project (read-only, do not modify):
  `u:\Git\DO.Support-UISimply\`.

## Success criterion

Trace file from one reproducing cell-bisect run, with annotated
analysis identifying:

1. The sequence of LSP frames that reference
   `AppExperience.Codeunit.al`.
2. Whether AL LS gets the file content via filesystem (during
   setActiveWorkspace) AND via didOpen, or via didOpen only and
   we need a different hypothesis.
3. Time between the two acquisition events (race window).

Annotate findings in `docs/issue-17/06-stdio-proxy.md`. Once
written, the issue-17 docs/ collection is enough to file a
high-quality MVP bug report with Microsoft.
