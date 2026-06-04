# Testing

## Prerequisites

- Python 3.x (for running test scripts only)
- Built binaries in platform-specific `bin/` directories

## Running Tests

From `test-al-project/`:

```bash
# Test the wrapper (all LSP operations)
python test_lsp_go.py --wrapper go

# Show wrapper logs after tests
python test_lsp_go.py --wrapper go --show-logs
```

## Test Coverage

### Wrapper Tests (`test_lsp_go.py`)

| Test | Description |
|------|-------------|
| Initialize | LSP initialization and project load |
| Hover | Hover information on symbols |
| Definition | Go to definition (translated to `al/gotodefinition`) |
| DocumentSymbol | List symbols in a file |
| WorkspaceSymbol | Search symbols across workspace |
| WorkspaceSymbol (empty) | Empty query returns [] + one-time warning (fallback for pre-2.1.x Claude Code) |
| WorkspaceSymbol (path) | Path-as-query fallback (older Claude Code clients) |
| References | Find all references to a symbol |
| CallHierarchy | Returns proper "not supported" error |

## Test Output

Successful run:
```
--- GO Results ---
  [+] PASS: Initialize - LSP initialized successfully
  [+] PASS: Hover - Got hover information
  [+] PASS: Definition - Found definition
  [+] PASS: DocumentSymbol - Found 1 symbol(s)
  [+] PASS: WorkspaceSymbol - Found 131 symbol(s)
  [+] PASS: WorkspaceSymbol (empty) - Returns [] (no retry-loop for Claude Code)
  [+] PASS: WorkspaceSymbol (path) - Path fallback worked
  [+] PASS: References - Found 4 reference(s)
  [+] PASS: CallHierarchy (unsupported) - Correctly returned MethodNotFound error

  Total: 9 passed, 0 failed
```

## Logs

Check wrapper logs for debugging:
- Windows: `%TEMP%\al-lsp-wrapper-go.log`
- Unix: `/tmp/al-lsp-wrapper-go.log`
