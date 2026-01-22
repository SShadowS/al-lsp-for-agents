# Testing

## Prerequisites

- Python 3.x (for running test scripts only)
- Built binaries in platform-specific `bin/` directories

## Running Tests

From `test-al-project/`:

```bash
# Test the wrapper (all LSP operations)
python test_lsp_go.py --wrapper go

# Test the launcher
python test_launcher.py

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
| WorkspaceSymbol (empty) | Empty query returns helpful error |
| WorkspaceSymbol (path) | Path-as-query workaround for Claude Code bug |
| References | Find all references to a symbol |
| CallHierarchy | Returns proper "not supported" error |

### Launcher Tests (`test_launcher.py`)

| Test | Description |
|------|-------------|
| Wrapper not found | Graceful error when wrapper missing from cache |
| Finds wrapper | Locates wrapper in plugin cache |
| Picks newest version | Correct semver sorting (17.x > 9.x) |
| Stdin/stdout passthrough | Full LSP roundtrip through launcher |

## Test Output

Successful run:
```
--- GO Results ---
  [+] PASS: Initialize - LSP initialized successfully
  [+] PASS: Hover - Got hover information
  [+] PASS: Definition - Found definition
  [+] PASS: DocumentSymbol - Found 1 symbol(s)
  [+] PASS: WorkspaceSymbol - Found 131 symbol(s)
  [+] PASS: WorkspaceSymbol (empty) - Correctly returned error for empty query
  [+] PASS: WorkspaceSymbol (path) - Path workaround worked
  [+] PASS: References - Found 4 reference(s)
  [+] PASS: CallHierarchy (unsupported) - Correctly returned MethodNotFound error

  Total: 9 passed, 0 failed
```

## Logs

Check wrapper logs for debugging:
- Windows: `%TEMP%\al-lsp-wrapper-go.log`
- Unix: `/tmp/al-lsp-wrapper-go.log`
