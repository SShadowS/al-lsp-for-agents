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

# Extra symbol package folders (.app files), e.g. a shared cache in CI.
# Path-list separated (":" on Linux, ";" on Windows); appended after the
# project's own and ancestor .alpackages. Missing folders are skipped.
AL_LSP_PACKAGE_CACHE=/cache/al-symbols/28 ./al-lsp-wrapper

# Resolve dependencies from source: folders scanned (up to 6 levels deep, no
# symlinks, no .alpackages) for AL projects. A dependency that one of them
# satisfies (same app id, version >= required) is loaded from source, the
# way VS Code handles project references in a multi-root workspace. Source
# wins over a .app of the same app. Every referenced project is compiled, so
# memory grows with the closure.
AL_LSP_SOURCE_ROOTS=/workspace/session ./al-lsp-wrapper
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
