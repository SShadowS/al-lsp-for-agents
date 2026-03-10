# Bug: LSP workspaceSymbol operation sends empty query parameter

## Description

The Claude Code LSP tool's `workspaceSymbol` operation does not pass a search query to the language server, making it non-functional. The tool sends `{"query": ""}` (empty string) instead of a user-provided search term.

## Expected Behavior

`workspaceSymbol` should accept a search query parameter (e.g., "Customer", "Sales") and pass it to the language server to search for symbols across the workspace.

## Actual Behavior

The LSP tool sends an empty query:
```json
{"query": ""}
```

This results in no symbols being found, regardless of the codebase content.

## Evidence

From the AL Language Server wrapper logs:
```
handle_workspace_symbol called
DEBUG params: {'query': ''}
Workspace symbol query: ''
```

## Root Cause

The LSP tool interface appears designed for file+position operations (filePath, line, character), which work correctly for:
- `findReferences` - find usages at a position
- `goToDefinition` - jump to definition at a position
- `documentSymbol` - list symbols in a file
- `hover` - get info at a position

However, `workspaceSymbol` requires a different parameter - a search query string - which isn't exposed in the current tool interface.

## Suggested Fix

Add a `query` parameter to the LSP tool for `workspaceSymbol` operations. Example interface:

```
LSP(operation: "workspaceSymbol", query: "Customer")
```

Or alternatively, repurpose an existing parameter when operation is `workspaceSymbol`.

## Workaround

Users can use `documentSymbol` to list symbols in a specific file, or use `Grep` to search for symbol names across the workspace.

## Steps to Reproduce

### Method 1: Direct LSP Tool Usage
1. Set environment variable `ENABLE_LSP_TOOL=1`
2. Open Claude Code in a project with an LSP configured
3. Ask Claude: "Use the LSP workspaceSymbol operation to find symbols named 'Customer'"
4. Claude will call: `LSP(operation: "workspaceSymbol", file: "some/file.al")`
5. **Result:** 0 symbols returned

### Method 2: Via AL LSP Wrapper (with logging)
1. Install the AL LSP wrapper from `SShadowS/al-lsp-for-agents`
2. Check the log file at `%TEMP%/al-lsp-wrapper.log`
3. Ask Claude to search for workspace symbols
4. **Log shows:**
   ```
   handle_workspace_symbol called
   DEBUG params: {'query': ''}
   Workspace symbol query: ''
   ```
5. The query parameter is empty - Claude Code never passes the search term

### Method 3: Compare with Working Operations
1. `documentSymbol` works: `LSP(operation: "documentSymbol", file: "path/to/file.al")` → Returns symbols
2. `findReferences` works: `LSP(operation: "findReferences", file: "...", line: 10, character: 5)` → Returns references
3. `workspaceSymbol` fails: Always returns 0 symbols regardless of codebase content

## Environment

- Claude Code version: Latest
- Language Server: AL Language Server (Microsoft Dynamics 365 Business Central)
- OS: Windows 11
- Also reproducible with other LSP servers that support workspaceSymbol
