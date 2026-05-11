# Changelog

All notable changes to AL LSP for Agents are documented here.

## [1.11.0] - 2026-05-11

### Fixed
- **AL Language Server silent hang on `al/symbolSearch`** -- when transitive dependency `.app` files (e.g. Microsoft Base Application required by Continia Core) were not in the project's own `.alpackages`, MS AL LSP threw an unhandled `NullReferenceException` in `SymbolSearchService.SymbolDescriptor.Create` and never sent a JSON-RPC response. The wrapper now walks ancestor directories for additional `.alpackages` folders and adds them to `packageCachePaths` so transitive deps stay resolvable. Stops at `.git` boundary or filesystem root. Captured stack trace + Microsoft issue draft in `docs/al-lsp-bugs/silent-symbolsearch-crash.md`.
- **Empty `workspace/symbol` queries no longer return an error** -- Claude Code's LSP tool surface has no `query` parameter on `workspaceSymbol`, so agents previously retry-looped on the `-32602` error. Returns `[]` instead, and fires a one-shot `window/showMessage` warning so the user sees the upstream Claude Code limitation.

### Added
- **`documentSymbol` event-publisher overlay (local files)** -- procedures decorated with `[IntegrationEvent]`, `[BusinessEvent]`, or `[InternalEvent]` are tagged `SymbolKind.Event` (24) with the attribute name prepended to their detail string. Makes event publishers immediately distinguishable from regular procedures in agent symbol enumeration.
- **`documentSymbol` synthesis for dependency objects** -- when MS AL LSP returns empty for `al-preview:/` virtual URIs (a known limitation), the wrapper synthesizes a complete `DocumentSymbol[]` from the `.app` archive's `SymbolReference.json` data. Includes full method signatures, parameter types, and event-publisher tagging. Tested against Base Application's `Approvals Mgmt.` codeunit (258 methods, 137 IntegrationEvents).
- **Hover enrichment on event-name literals** -- hovering on `'OnAfterPostApprovalEntries'` inside an `[EventSubscriber(...)]` attribute returns the publisher's full signature, attribute kind, and source app metadata, all in a single hover call. Multi-line attributes (one-argument-per-line formatting) and embedded comments are supported.

### Changed
- Updated al-call-hierarchy to v0.8.0 (ancestor `.alpackages` walk, event-aware indexing, new RPCs for the wrapper's hover/documentSymbol enrichment paths).

## [1.10.2] - 2026-04-30

### Fixed
- **Misleading "Another instance is already running" warning** -- the duplicate-instance detector now identifies which two clients spawned conflicting wrappers (VS Code extension, Claude Code plugin, or unidentified) and gives concrete steps to disable each. PID-reuse false positives are eliminated by verifying the live process is actually `al-lsp-wrapper`. Lock file format upgraded to JSON with backwards-compatible reads of legacy raw-PID files. (#18)

### Added
- **`alLspForAgents.verboseDiagnosticLogging` setting** -- when enabled, the middleware dumps every diagnostic (source, code, severity, line, message) to the output channel for investigating issues #15/#17.

### Changed
- Updated al-call-hierarchy to v0.6.0 (V2 grammar follow-up: expanded LSP surface, parser refinements).
- Build uses `-trimpath` for reproducible binaries.

## [1.10.1] - 2026-04-08

### Fixed
- **Virtual URI handling for dependency apps** -- LSP operations (definition, hover, documentSymbol, references) now work on files from dependency .app packages that use `al-preview:` URIs, instead of failing with "File does not exist".

## [1.10.0] - 2026-04-07

### Added
- **Auto-download AL Language extension** -- the wrapper can now automatically download and manage the Microsoft AL Language extension from the VS Code Marketplace, enabling use in environments without VS Code (Sublime Text, standalone). Controlled via `--auto-download-al-extension` flag.
- **Release channel selection** -- choose between release and prerelease versions of the AL extension with `--al-extension-channel release|prerelease`.
- **Background update checks** -- when auto-download is enabled, the wrapper checks for extension updates once per day in the background. Use `--force-update-al-extension` to update immediately.

## [1.9.9] - 2026-03-28

### Fixed
- **Extension conflict with AL Test Runner** -- added `--vscode` flag that disables `textDocumentSync` in server capabilities, preventing other extensions from flooding the wrapper's AL LSP with document events. (#17)
- **Duplicate didOpen notifications** -- the wrapper now tracks files opened via VS Code's LanguageClient in `openedFiles`, so `EnsureFileOpened` no longer sends a second `didOpen` for files already synced.

### Changed
- Updated al-call-hierarchy to v0.5.0.

## [1.9.8] - 2026-03-22

### Added
- **`symbolName` parameter** for position-based tools (`bclsp_goToDefinition`, `bclsp_hover`, `bclsp_findReferences`) -- when provided alongside a position, the wrapper verifies the symbol at that position matches the expected name, reducing false results from stale line numbers.

## [1.9.7] - 2026-03-22

### Fixed
- **Reverted document sync suppression** -- suppressing `didOpen`/`didClose` via middleware caused the LanguageClient to shut down the server 300ms after initialization. Reverted to allowing document sync through. (#16)

## [1.9.3-1.9.6] - 2026-03-21

### Fixed
- **Duplicate "Object Already Declared" diagnostics** -- multiple fixes to prevent false compiler errors when running alongside the MS AL extension in VS Code. (#15)
  - Filter compiler diagnostics in the VS Code middleware (only show `al-call-hierarchy` diagnostics).
  - Disable `backgroundCodeAnalysis` in the wrapper to prevent unnecessary compilation.
  - Detect duplicate wrapper instances on the same workspace and warn the user.
  - Remove `semanticTokensProvider` from capabilities (caused crashes in the AL LSP).
  - Remove `executeCommandProvider` to avoid command registration conflicts.

## [1.9.2] - 2026-03-21

### Fixed
- **Multi-root workspace crash** — the wrapper crashed with `cannot unmarshal string into Go struct field Message.error of type wrapper.RPCError` when the AL Language Server returned a plain string error instead of a JSON-RPC error object. The custom `UnmarshalJSON` now handles both formats. (#13)
- **Workspace folder state not updated on add/remove** — `al/didChangeWorkspaceFolders` forwarded the notification but never updated the wrapper's internal folder list, causing stale state.
- **app.json search only checked first workspace folder** — `handleInitialize` now searches all workspace folders for an AL project, not just the `rootUri`.
- **Hardcoded folder index in workspace activation** — `al/setActiveWorkspace` now sends the correct folder index instead of always `0`.
- **Format mismatch for al-call-hierarchy notifications** — `workspace/didChangeWorkspaceFolders` now wraps params in the standard LSP `{event: ...}` format when forwarding to al-call-hierarchy.

## [1.9.1] - 2026-03-16

### Changed
- **Code quality diagnostics are now opt-in** — disabled by default in the VS Code editor. Enable via `alLspForAgents.enableCodeQualityDiagnostics` setting. The `bclsp_codeQualityDiagnostics` tool and Claude Code diagnostics are unaffected.

### Added
- New VS Code setting: `alLspForAgents.enableCodeQualityDiagnostics` (default: `false`).

## [1.9.0] - 2026-03-16

### Added
- **Multi-root workspace support** — all AL project folders in a multi-root workspace now receive proper LSP support, not just the first one. The extension automatically switches the active workspace when tool requests target files in different folders.
- Go wrapper forwards `workspaceFolders` to both the AL Language Server and al-call-hierarchy during initialization.
- Workspace folder add/remove events forwarded via `al/didChangeWorkspaceFolders`.

## [1.8.2] - 2026-03-16

### Removed
- Removed `bclsp_workspaceSymbols` and `bclsp_symbolSearch` from the VS Code extension — redundant with the MS AL extension's `al_symbolsearch` tool which is always available in VS Code and has more filters. The Go wrapper still handles `workspace/symbol` for Claude Code users.

## [1.8.1] - 2026-03-16

### Fixed
- **workspace/symbol query filtering** — the Go wrapper was sending `{"filter": "..."}` but the AL Language Server expects `{"query": "..."}`. Queries now return filtered results instead of the first 200 unfiltered symbols.

### Added
- `bclsp_symbolSearch` tool exposing the full `al/symbolSearch` capabilities including member-level search via `filters.memberKinds`, `filters.objectName`, and `filters.kinds`.

## [1.8.0] - 2026-03-16

### Added
- `bclsp_documentSymbols` — get the hierarchical symbol tree for an AL file (objects, procedures, fields, actions).
- `bclsp_workspaceSymbols` — search for symbols across the workspace by name.
- `bclsp_renameSymbol` — rename a symbol at a position, applying changes across all files.

### Changed
- Updated esbuild to 0.27.4, @types/node to 25.5.0.

## [1.7.0] - 2026-03-16

### Changed
- **Renamed all tool prefixes from `al_` to `bclsp_`** to avoid collision with Microsoft's AL Language Extension tools which also use `al_*`. The `bclsp_` prefix ("BC LSP") uniquely identifies these tools without reading descriptions.
- Updated al-call-hierarchy to v0.4.3 (fixes URI percent-decoding on Windows).

### Breaking
- All tool names changed: `al_goToDefinition` → `bclsp_goToDefinition`, etc. Update any instruction files or prompt configurations that reference the old names.

## [1.6.2] - 2026-03-14

### Fixed
- Updated al-call-hierarchy with fix for case-sensitive path lookup on Windows.

## [1.6.1] - 2026-03-11

### Fixed
- Fixed EMFILE (too many open files) exhaustion by disabling redundant file watcher that conflicted with the LanguageClient's built-in file watching.

## [1.6.0] - 2026-03-11

### Added
- Code quality diagnostics: unused procedures, high cyclomatic complexity, too many parameters, high fan-in, long methods.
- Code lens showing reference counts per procedure.
- Configurable diagnostic thresholds via global (`~/.al-call-hierarchy/config.json`) and per-workspace (`.al-call-hierarchy.json`) configuration.

## [1.5.2] - 2026-03-10

### Added
- Enriched hover with XML doc comments, field properties, and action properties extracted via tree-sitter.
- Configurable diagnostic thresholds with per-category enable/disable flags.

## [1.5.0] - 2026-03-10

### Added
- Initial VS Code Marketplace release.
- Language Model Tools for GitHub Copilot agent mode: `al_goToDefinition`, `al_hover`, `al_findReferences`, `al_prepareCallHierarchy`, `al_incomingCalls`, `al_outgoingCalls`, `al_codeLens`, `al_codeQualityDiagnostics`.
- Go wrapper combining Microsoft AL Language Server with al-call-hierarchy.
- Bundled binaries for Windows and Linux — no additional installation needed.
