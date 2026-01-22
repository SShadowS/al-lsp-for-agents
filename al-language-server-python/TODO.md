# AL Language Server Wrapper - TODO

## Current State

The AL LSP wrapper (`al_lsp_wrapper.py`) provides integration between Claude Code and the Microsoft AL Language Server from the VS Code extension.

### Working Features

- **documentSymbol**: Returns full symbol tree with tables, fields, procedures, etc.
- **Hover**: Returns markdown-formatted hover info
- **goToDefinition**: Works! Returns file URI and range for symbol definitions
- **findReferences**: Works! Returns all references across project files (pass-through)
- **goToImplementation**: Works! Returns implementation locations (pass-through)
- **prepareCallHierarchy**: Works! Returns call hierarchy item at position
- **incomingCalls**: Works! Finds callers of a procedure
- **outgoingCalls**: Works! Finds calls made by a procedure
- **Initialization**: Properly initializes with AL-specific params, opens app.json, sets active workspace
- **Auto-detection**: Automatically finds AL projects by searching for app.json in workspace
- **Multi-project support**: Routes requests to correct project based on file URI (walks up to find app.json)

### Known Bugs

- **workspaceSymbol**: Claude Code's LSP tool doesn't pass the required `query` parameter, causing empty results. Use `documentSymbol` or `Grep` as workarounds.

## Potential Improvements

### Medium Priority

1. **Add completion support**
   - AL LSP supports completions with triggers: `.`, `:`, `"`, `/`, `<`
   - Add `handle_completion()` with file opening

2. **Improve logging**
   - Add log levels (debug, info, error)
   - Log to stderr for debugging while keeping stdout clean for LSP

### Low Priority

3. **Handle workspace/configuration requests**
   - AL LSP may request configuration dynamically
   - Currently only sent during post-initialization

## Testing

Run the test script:
```bash
cd test-al-project
python test_lsp.py
```

Check wrapper log:
```bash
cat "$TEMP/al-lsp-wrapper.log"
```

## References

- Serena AL implementation: `U:\Git\serena\src\solidlsp\language_servers\al_language_server.py`
- LSP Specification: https://microsoft.github.io/language-server-protocol/
- AL Language Extension: `ms-dynamics-smb.al` in VS Code marketplace
