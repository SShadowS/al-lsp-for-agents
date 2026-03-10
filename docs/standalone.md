# Standalone Usage (Codex / Other Agents)

## Download

Download the latest binaries from [GitHub Releases](https://github.com/SShadowS/al-lsp-for-agents/releases):

- **Windows:** `al-lsp-wrapper-windows-x64.zip`
- **Linux:** `al-lsp-wrapper-linux-x64.tar.gz`

Each archive contains:
- `al-lsp-wrapper` — the main LSP wrapper
- `al-call-hierarchy` — call hierarchy / code lens sidecar

## Prerequisites

- The Microsoft AL Language extension for VS Code must be installed
  (the wrapper uses its language server binary)

## Usage

Run the wrapper over stdio:

```bash
# Auto-discover the AL extension
./al-lsp-wrapper

# Or specify the extension path explicitly
./al-lsp-wrapper --al-extension-path /path/to/ms-dynamics-smb.al-17.x.x

# Or via environment variable
AL_EXTENSION_PATH=/path/to/extension ./al-lsp-wrapper
```

The wrapper speaks standard LSP over stdin/stdout. It:
- Translates `textDocument/definition` to `al/gotodefinition` (the AL-specific variant)
- Provides call hierarchy via `al-call-hierarchy` sidecar
- Provides code lens (reference counts) via `al-call-hierarchy`
- Publishes code quality diagnostics (unused procedures, complexity, etc.)

## Codex CLI

Codex CLI does not currently support LSP integration. For now:

- **Codex IDE users** (VS Code / Cursor): Install the VS Code extension from the marketplace
- **Codex CLI users**: These binaries are ready for when LSP support is added

## Supported LSP Methods

| Method | Routed To |
|--------|-----------|
| `textDocument/definition` | Microsoft AL LSP (`al/gotodefinition`) |
| `textDocument/hover` | Microsoft AL LSP |
| `textDocument/references` | Microsoft AL LSP |
| `textDocument/documentSymbol` | Microsoft AL LSP |
| `workspace/symbol` | Microsoft AL LSP |
| `textDocument/prepareCallHierarchy` | al-call-hierarchy |
| `callHierarchy/incomingCalls` | al-call-hierarchy |
| `callHierarchy/outgoingCalls` | al-call-hierarchy |
| `textDocument/codeLens` | al-call-hierarchy |
| `textDocument/publishDiagnostics` | Both (merged) |
