# Known Issues

## al_symbolrelations MCP Tool Crashes (upstream Microsoft DI bug)

**Status:** Open upstream — wrapper works around it.
**Affected:** `almcp` 18.0.37 and the AL extension-bundled `almcp`.

### Problem

Microsoft's `al_symbolrelations` MCP tool throws on every call:

```
InvalidOperationException: No service for type
'...SymbolRelationsService' has been registered
```

The service backing the tool is never registered in the MCP server's
dependency-injection container, so the tool is non-functional in both the
nuget `al` dotnet tool (`almcp` 18.0.37) and the AL VS Code extension's
bundled `almcp`.

### Wrapper behavior

`al/symbolRelations` (tool `bclsp_symbolRelations`) tries the MCP
`al_symbolrelations` tool first, and on this failure automatically falls back
to the inner AL Language Server's native `al/symbolRelations` method, which
works. Once Microsoft fixes the MCP tool, the wrapper will prefer it again
without any change.

### Upstream report

Drafted bug report in [docs/ms-almcp-symbolrelations-di-bug.md](docs/ms-almcp-symbolrelations-di-bug.md).

## workspaceSymbol Returned Empty Results (RESOLVED in Claude Code 2.1.x)

**Status:** Resolved — fixed client-side in Claude Code 2.1.x.
**Upstream:** https://github.com/anthropics/claude-code/issues/17149

### Problem (historical)

The `workspaceSymbol` LSP operation always returned 0 symbols when called
through Claude Code's LSP tool, because the client hardcoded an empty query:

```json
{"query": ""}
```

Every other operation derived `{textDocument, position}` from the
`filePath`/`line`/`character` args, but `workspaceSymbol` alone sent neither
and no `query` field — so the AL LSP (which rejects empty queries) returned
nothing regardless of the codebase.

### Resolution

Claude Code 2.1.x exposes a `query` parameter on the LSP tool's
`workspaceSymbol` operation and passes it through. Verified on 2.1.162:

```
Received from client: method=workspace/symbol id=1
Sending al/symbolSearch for query: Customer   → matches returned
```

No wrapper change was required for the fix itself. The wrapper's empty-query
handling is kept as a defensive fallback for older Claude Code versions and
non-Claude LSP clients.

### Wrapper behavior

1. **Empty-query guard** (`WorkspaceSymbolHandler`) — a blank query returns
   `[]` (not an error, so agents don't retry-loop) plus a one-time
   `window/showMessage` warning. With Claude Code 2.1.x+ this is rarely hit.

2. **File path extraction** (`ExtractSymbolFromPath`) — if a file path is
   passed as the query (observed with older clients), the wrapper extracts the
   symbol name: `Table 6175301 CDO File.al` → `"CDO File"`.

3. **al/symbolSearch with cold-index retry** — the wrapper routes the query to
   the AL LSP's `al/symbolSearch` (standard `workspace/symbol` deadlocks the AL
   LS). The AL LS builds its symbol index asynchronously after `initialize`, so
   the first search of a session can race the index and return `[]` even when
   the symbol exists. When a search returns 0 results and no search has yet
   succeeded this session, the wrapper retries with bounded backoff
   (~1.6s total) to ride out warmup. Once any search returns results, `0` is
   treated as a genuine miss with no further retries.

### Related fallbacks

- `documentSymbol(file)` — per-file symbols (always reliable).
- `goToDefinition` / `findReferences` — navigation from a known position.

### Version History

- **v1.2.0** - Added `handle_workspace_symbol` with project initialization
- **v1.2.2** - Fixed null result handling
- **v1.2.3** - Added file path to query extraction
- **v1.2.4** - Added helpful error message for empty query
- **Current** - Claude Code 2.1.x ships the upstream fix (#17149); wrapper
  empty-query path kept as fallback, reworded; added cold-index retry for
  `al/symbolSearch` to fix first-search-of-session races
