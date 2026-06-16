# AL LSP Comparison: Wrapper vs Microsoft's Servers vs LSP Spec

Date: 2026-06-15. Evidence captured by live probes (see [Reproducing](#reproducing) below).

## TL;DR

Microsoft now ships **two** AL language servers plus an MCP server. They are not the same:

| Server | What it is | Protocol | Who spawns it |
|---|---|---|---|
| `Microsoft.Dynamics.Nav.EditorServices.Host` | AL LS bundled in the VS Code extension (`ms-dynamics-smb.al`) | Custom `al/*` (`al/gotodefinition`, `al/setActiveWorkspace`, `al/symbolSearch`) | **This wrapper** (`paths.go:105`), and VS Code itself |
| `al launchlspserver` | NEW standalone dotnet tool, stdio, "for agentic AL development" | **Standard LSP** (`textDocument/definition`, …) | Anything that runs the `al` dotnet tool |
| `al launchmcpserver` | NEW MCP server (15 tools), stdio | MCP `2024-11-05` | MCP clients |

Both new servers ship in nuget `microsoft.dynamics.businesscentral.development.tools`. Probed against **18.0.37.11445-beta** (updated from 18.0.36 before testing).

Headline: **Microsoft's new standalone LSP targets the wrapper's exact lane but omits call hierarchy, code lens, code-quality diagnostics, and enriched hover — the wrapper's whole differentiator.** Separately, the two MS servers diverge sharply in standard-LSP coverage (table below).

## Versions probed

- AL dotnet tool: `18.0.37.11445-beta` (newest prerelease; newest stable is `17.0.34`)
- VS Code AL extension: `ms-dynamics-smb.al-18.0.2293710`
- Wrapper: this repo (al-lsp-for-agents 1.11.4) + al-call-hierarchy

## 1. Standard LSP capability matrix

Columns:
- **Spec** — feature exists in LSP spec
- **EditorServices** — raw capabilities of the server the wrapper wraps
- **Wrapper** — what the *client* actually sees (EditorServices, modified by `addExtraCapabilities`)
- **Standalone** — `al launchlspserver` raw capabilities

| Feature | Spec | EditorServices (wrapped) | Wrapper (client-facing) | Standalone `launchlspserver` |
|---|:---:|:---:|:---:|:---:|
| textDocumentSync | ✅ | 2 (incremental) | **0 (None)** in VS Code mode | 1 (full) |
| hover | ✅ | ✅ | ✅ **enriched** (tree-sitter props) | ✅ plain |
| completion | ✅ | ✅ (`. : " / <`) | ✅ passthrough | ✅ (`. "`) |
| signatureHelp | ✅ | ✅ (`(`) | ✅ passthrough | ✅ (`( ,`) |
| definition | ✅ | ❌ `false` (uses custom `al/gotodefinition`) | ✅ (wrapper bridges to `al/gotodefinition`) | ✅ **standard** |
| references | ✅ | ✅ | ✅ | ✅ |
| documentHighlight | ✅ | ✅ | ✅ passthrough | ✅ |
| documentSymbol | ✅ | ✅ | ✅ | ✅ |
| workspaceSymbol | ✅ | ✅ | ✅ (cold-index retry, NRE workaround) | ✅ |
| formatting / rangeFormatting | ✅ | ✅ both | ✅ passthrough | ✅ both |
| rename | ✅ | ✅ | ✅ | ✅ |
| codeAction | ✅ | ✅ | ✅ passthrough | ❌ `false` |
| **codeLens** | ✅ | ✅ native (resolve) | ✅ **replaced w/ al-call-hierarchy** (resolve off) | ❌ |
| **callHierarchy** | ✅ | ❌ | ✅ **added (al-call-hierarchy)** | ❌ |
| implementation | ✅ | ✅ | ✅ passthrough | ❌ `false` |
| typeHierarchy | ✅ | ✅ | ✅ passthrough | ❌ `false` |
| semanticTokens | ✅ | ✅ (range+full, empty legend) | ❌ **stripped** (MS impl NRE-crashes → 30s timeouts) | ❌ |
| executeCommand | ✅ | ✅ (`al/runCodeAction`) | ❌ **stripped** (avoids dup-registration clash) | (dynamic) |
| foldingRange | ✅ | ❌ `false` | ❌ | ✅ |
| inlayHint | ✅ | ❌ `false` | ❌ | ❌ `false` |
| **code-quality diagnostics** | ➖ non-spec | ❌ | ✅ unused/complexity/params/fan-in/long-method | ❌ |
| declaration / typeDefinition | ✅ | ❌ | ❌ | ❌ |
| documentLink / color / selectionRange | ✅ | ❌ | ❌ | ❌ |
| pull diagnostics (`textDocument/diagnostic`) | ✅ | ❌ (push only) | ❌ (push only) | ❌ (push only) |
| inlineValue / linkedEditing / moniker | ✅ | ❌ | ❌ | ❌ |

### What this reveals

- **The two MS servers are very different.** EditorServices has `codeAction`, `codeLens`, `implementation`, `typeHierarchy`, `semanticTokens` but **no `foldingRange`** and `definition:false` (custom `al/*`). Standalone is the mirror image: standard `definition` + `foldingRange`, but **drops** codeAction/codeLens/implementation/typeHierarchy/semanticTokens.
- **Wrapper-only vs both MS servers:** call hierarchy, code-quality diagnostics, enriched hover. (codeLens exists natively in EditorServices but the wrapper substitutes its own al-call-hierarchy implementation.)
- **Wrapper passthrough wins the wrapped client gets for free:** implementation, typeHierarchy, codeAction, completion, signatureHelp, documentHighlight, formatting — all served by EditorServices, none exposed by the standalone server.
- **Nobody supports:** pull diagnostics, inlayHint, declaration, typeDefinition, documentLink, color, selectionRange, inlineValue, linkedEditing, moniker.

## 2. Agentic tooling: MS MCP vs Wrapper's VS Code LM tools

Different philosophies. MS MCP = **build/deploy lifecycle**. Wrapper LM tools = **code navigation/analysis**.

| Capability | MS MCP `launchmcpserver` (15) | Wrapper VS Code LM tools (12) |
|---|:---:|:---:|
| go to definition | ❌ | ✅ `bclsp_goToDefinition` |
| hover (enriched) | ❌ | ✅ `bclsp_hover` |
| find references | ❌ | ✅ `bclsp_findReferences` |
| call hierarchy (prepare/in/out) | ❌ | ✅ ×3 |
| code lens | ❌ | ✅ `bclsp_codeLens` |
| code-quality diagnostics | ❌ | ✅ `bclsp_codeQualityDiagnostics` |
| document symbols | ❌ | ✅ `bclsp_documentSymbols` |
| rename symbol | ❌ | ✅ `bclsp_renameSymbol` |
| symbol search (deps, no source) | ✅ `al_symbolsearch` | ➖ workspaceSymbol (project scope only) |
| **symbol relations** (extends/implements/sourcetable) | ✅ `al_symbolrelations` | ✅ `bclsp_symbolRelations` (MCP, EditorServices fallback) |
| compile / build / get diagnostics | ✅ `al_compile` `al_build` `al_getdiagnostics` | ❌ |
| download symbols | ✅ `al_downloadsymbols` | ❌ |
| publish / run tests | ✅ `al_publish` `al_run_tests` | ❌ |
| inspect page (control/action tree) | ✅ `al_inspectpage` | ✅ `bclsp_inspectPage` (requires nuget al tool) |
| translations (XLIFF search/write) | ✅ `al_searchtranslations` `al_writetranslation` | ❌ |
| auth (Entra ID) | ✅ `al_auth_login` `al_auth_logout` | ❌ |
| add project / package deps | ✅ `al_addproject` `al_getpackagedependencies` | ❌ |

- MS MCP has **zero** call-graph / reference navigation. Pure compile-deploy-symbolsearch.
- Wrapper has **zero** build/compile/publish/test/download-symbols.
- Symbol relations and page inspection are now implemented in the wrapper (`bclsp_symbolRelations`, `bclsp_inspectPage`). The wrapper owns its own `almcp` MCP backend, preferring the nuget `al` dotnet tool (15 tools incl. `al_inspectpage`) and falling back to the extension-bundled `almcp` (12 tools, no `al_inspectpage`). `bclsp_inspectPage` requires the nuget `al` tool. `bclsp_symbolRelations` tries the MCP tool first but Microsoft's `al_symbolrelations` is currently broken by an upstream DI bug (`SymbolRelationsService` not registered), so the wrapper falls back to the inner AL LS native `al/symbolRelations` (see [ms-almcp-symbolrelations-di-bug.md](ms-almcp-symbolrelations-di-bug.md)).

## 3. Takeaways

1. **No collision on release.** Neither new MS server touches call hierarchy, code lens, or code-quality. The differentiator survives.
2. **Wrapper wraps the old server.** It spawns EditorServices (custom `al/*`), not the new standard `launchlspserver`. Re-targeting the standalone would give a cleaner protocol, stdio-native, no VS-Code-extension dependency, and dodge the semantic-tokens NRE — **but** the standalone drops codeAction/implementation/typeHierarchy/codeLens and keeps no `al/symbolSearch`, so passthrough coverage would shrink. Net: not a free win.
3. **Implemented from MS MCP:** `al_symbolrelations` and `al_inspectpage` are no longer a gap — the wrapper now exposes both as `bclsp_symbolRelations` and `bclsp_inspectPage` (LSP methods `al/symbolRelations` and `al/inspectPage`), backed by its own `almcp` MCP server (nuget `al` tool preferred, extension-bundled `almcp` fallback). `inspectPage` requires the nuget `al` tool. `symbolRelations` runs via the MCP tool with an EditorServices native fallback, because Microsoft's `al_symbolrelations` MCP tool is broken by an upstream DI bug (`SymbolRelationsService` not registered); see [ms-almcp-symbolrelations-di-bug.md](ms-almcp-symbolrelations-di-bug.md). The wrapper will prefer the MCP tool automatically once Microsoft fixes it.
4. **Positioning vs MS:** "they build & deploy, we understand the code."

## Reproducing

From `test-al-project/` (Windows, PowerShell):

```powershell
python probe_ms_official_lsp.py       # -> ms-official-lsp-capabilities.json (standalone launchlspserver)
python probe_ms_mcp.py                # -> ms-official-mcp-tools.json (15 MCP tools)
python probe_editorservices_lsp.py    # -> editorservices-lsp-capabilities.json (wrapped server)
```

Update the AL dotnet tool first:

```powershell
dotnet tool update --global microsoft.dynamics.businesscentral.development.tools --prerelease
```
