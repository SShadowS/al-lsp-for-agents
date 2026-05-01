# Issue #17 — AL0264 / AL0197 false positives on Windows

This directory documents an investigation into false-positive
duplicate-declaration errors emitted by the Microsoft AL Language
Server.

**Filed upstream:** <https://github.com/microsoft/AL/issues/8249>
(related to the closed `microsoft/AL#6077`, which fixed the
file-rename variant of the same symptom in BC 22.0).

## The bug in one paragraph

When VS Code sends `textDocument/didOpen` with a file URI whose path
casing differs from the on-disk casing (for example
`Cloud/Al/Activation/AppExperience.Codeunit.al` vs the actual on-disk
`Cloud/AL/Activation/AppExperience.Codeunit.al`), the AL Language
Server treats the editor URI and its own filesystem-walked URI as two
distinct files. Both produce a `SingleObjectDeclaration` for the same
object, the SymbolMap ends up with two `ContainerSymbol` entries for
one logical codeunit, and `CheckForDuplicateIds` correctly fires
AL0264 / AL0197 against the project itself.

Verified on `ms-dynamics-smb.al` 18.0.2190758, VS Code 1.103.0,
Windows 11. Reproduces 5 out of 5 runs in the harness in this repo.

## Symptom

```
AL0197  An application object of type 'Codeunit' with name '<Name>'
        is already declared by the extension '<own project name>'
AL0264  An application object of type 'Codeunit' with ID '<id>'
        is already declared by the extension '<own project name>'
```

The "already declared by extension X" name in the message is the open
project itself.

## Steps to reproduce

1. AL workspace on Windows containing at least one object whose
   declaring file lives in a directory like `Cloud/AL/Activation/`.
2. Open the file via a URI that uses different casing for any path
   component, for example `Cloud/Al/Activation/...` (lowercase l)
   instead of `Cloud/AL/Activation/...` (uppercase L). Windows
   resolves the path case-insensitively, so the file opens normally.
3. Wait for the AL Language Server to publish diagnostics.

This can be triggered organically by:
- Multi-root workspaces where one root spells a child path with
  different casing than another.
- Extensions that issue `didOpen` from cached or normalized paths
  (test runners, navigation aids, language server wrappers).
- Quick Open / workspace symbol search that surfaces paths from
  indexes whose casing has been folded.

## Root cause (decompilation evidence)

The AL Language Server keys `SyntaxTree` / `SingleObjectDeclaration`
instances by raw path string, without case-folding on Windows. Two
strings that the OS treats as the same file end up as separate
logical entities inside the compiler.

The chain that produces the duplicate symbol is, end to end:

| Site | What happens |
|------|-------------|
| (FS scan during `al/setActiveWorkspace`) | LS walks the workspace, parses each `.al` file, creates `SyntaxTree` A using the on-disk path casing |
| (`textDocument/didOpen` for the same file, casing differs) | LS parses the editor buffer, creates `SyntaxTree` B using the editor's casing — A and B both end up in the Compilation |
| `MergedNamespaceDeclaration.MakeChildren` | dedups by identity, keeps both `SingleObjectDeclaration`s under one `MergedObjectDeclaration` with two `SyntaxReferences` |
| `SourceNamespaceSymbol.BuildObjectSymbolsFromDeclaration` (SourceNamespaceSymbol.cs:454) | iterates `declaration.SyntaxReferences` and creates one `SourceCodeunitTypeSymbol` per reference. **This is the un-merge step.** |
| `SourceModuleSymbol.CheckForDuplicateIds` (SourceModuleSymbol.cs:768) | correctly observes two `ContainerSymbol`s with id `6225384` |
| `SourceModuleSymbol.ReportDuplicateId` (SourceModuleSymbol.cs:1075) | emits `ERR_AppObjDuplicateId` (AL0264) |
| `SourceModuleSymbol.ReportDuplicateNameErrorForSymbol` (SourceModuleSymbol.cs:743) | emits `ERR_AppObjDuplicateName` (AL0197) |

Decompilation source: `Microsoft.Dynamics.Nav.CodeAnalysis.dll` from
`%USERPROFILE%\.vscode\extensions\ms-dynamics-smb.al-18.0.2293710\bin\win32\`,
disassembled with `ilspycmd 10.0.0.8330`.

## Frame-level evidence from a stdio proxy

A stdio proxy that sits between VS Code and
`Microsoft.Dynamics.Nav.EditorServices.Host.exe` and logs every
JSON-RPC frame as NDJSON captured the following (full timeline in
`06-stdio-proxy.md`):

```
22:39:22.448  in  al/setActiveWorkspace      (no file list in params)
22:39:22.481  in  textDocument/didOpen
              uri = file:///.../Cloud/Al/Activation/AppExperience.Codeunit.al
                                       ^^ lowercase, as supplied by editor
22:39:22.514  in  al/didChangeActiveDocument (same lowercase URI)
              ... (33 ms race window between the FS-scan trigger and the
                   didOpen for the same physical file) ...
```

Final diagnostics arrive on **both** casings:
- 13 diagnostics under `Cloud/Al/Activation/...` (the didOpen URI),
  including AL0264 + AL0197 at line 0.
- 169 diagnostics under `Cloud/AL/Activation/...` (the FS-scan URI),
  including the AL0185 errors expected for an unopened file in the
  same project.

`al/setActiveWorkspace` carries no file list. The duplicate-tree
state therefore originates inside the LS itself when its workspace
walk records files with on-disk casing while editor input arrives
with caller casing.

## Suggested fix

On Windows, canonicalise file URIs to their on-disk casing (or
compare them case-insensitively) before they reach the `SyntaxTree`
cache and before declarations are grouped in
`MergedNamespaceDeclaration.MakeChildren`. The most defensive option
is to resolve every incoming `textDocument/didOpen` URI through
`GetFinalPathNameByHandle` so the LS uses one canonical form
everywhere.

A more localized alternative: change
`BuildObjectSymbolsFromDeclaration` (SourceNamespaceSymbol.cs:454)
to produce one symbol per `MergedObjectDeclaration` instead of one
per `SyntaxReference`. The existing duplicate-id check would then
fire only for genuine multi-source-file declarations (which AL
doesn't allow, so the diagnostic still makes sense in that case).

## Reproduction harness

This repo's `harness/` directory builds a deterministic harness
around `@vscode/test-electron`. The minimal reproducing fixture is
11 AL files under `Cloud/AL/Activation/` (real Continia code, not
redistributable here). The specific cell that drives it is
`harness/cells/cell-bisect.ts`.

## Files in this directory

The other files in this directory are session-by-session
investigation notes. They include false leads, hypotheses that
were later falsified, and project-specific harness details. Read
them only if you are continuing the investigation, not for an
overview of the bug.

- `00-investigation-log.md` — chronological log of every experiment.
- `02-decompilation-notes.md` — full decompilation walkthrough with
  more code than the table above. Source of truth for the symbol
  references.
- `03-minimized-repro.md` — bisection trail, file list, and
  harness-specific repro recipe.
- `04-wire-trace-analysis.md` — earlier analysis of the AL verbose
  log; superseded by `06-stdio-proxy.md` once the proxy made the
  race directly observable.
- `06-stdio-proxy.md` — stdio proxy design and the trace that
  pinpointed the URI-casing root cause.
- `HANDOFF-stdio-proxy.md` — internal handoff doc for the proxy
  build session.
- `msbug-body.md` — local snapshot of the body filed at
  `microsoft/AL#8249` (the public issue is the source of truth).
