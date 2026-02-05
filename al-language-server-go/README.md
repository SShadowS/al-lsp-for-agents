# AL Language Server Wrapper (Go)

A Go implementation of the AL Language Server wrapper for Claude Code. This provides the same functionality as the Python wrapper but as static binaries with **no runtime dependencies** (no Python, no PowerShell).

## Building

### Prerequisites

- Go 1.21 or later

### Build Commands

```bash
cd al-language-server-go

# Windows
go build -ldflags="-s -w" -o bin/al-lsp-wrapper.exe .

# Linux
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/al-lsp-wrapper-linux .

# macOS
GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o bin/al-lsp-wrapper-darwin .
```

### Platform-Specific `.lsp.json`

The `.lsp.json` must reference the correct wrapper binary for the platform:

| Platform | Command |
|----------|---------|
| Windows | `${CLAUDE_PLUGIN_ROOT}/bin/al-lsp-wrapper.exe` |
| Linux | `${CLAUDE_PLUGIN_ROOT}/bin/al-lsp-wrapper` |
| macOS | `${CLAUDE_PLUGIN_ROOT}/bin/al-lsp-wrapper` |

## Binaries

| Binary | Size | Purpose |
|--------|------|---------|
| `al-lsp-wrapper.exe` | ~2.7 MB | Main LSP wrapper that communicates with AL LSP |

## Installation

1. Build the binaries (see above)
2. Install via Claude Code marketplace
3. The `.lsp.json` points directly to the wrapper binary - no external dependencies

## Features

- **No runtime dependencies** - no Python, no PowerShell, just native Go binaries
- Same functionality as the Python wrapper:
  - Auto-detects AL projects via app.json
  - Translates `textDocument/definition` to `al/gotodefinition`
  - Handles file opening requirements automatically
  - Initializes workspaces and waits for project load
  - Supports hover, documentSymbol, references, workspaceSymbol
  - Workaround for Claude Code's workspace/symbol query bug
  - Proper semver sorting to find newest AL extension (e.g., 17.x > 9.x)

## Logging

Logs are written to:
- Windows: `%TEMP%\al-lsp-wrapper-go.log`
- Unix: `/tmp/al-lsp-wrapper-go.log`

## Architecture

```
al-language-server-go/
├── main.go              # Wrapper entry point
├── wrapper/
│   ├── jsonrpc.go       # JSON-RPC message parsing/writing
│   ├── handlers.go      # LSP method handlers
│   ├── project.go       # Project detection and initialization
│   ├── paths.go         # Path utilities
│   └── wrapper.go       # Main wrapper logic
└── bin/
    └── al-lsp-wrapper.exe   # Wrapper binary
```

### How it works

1. Claude Code runs `al-lsp-wrapper.exe` (from `.lsp.json`)
2. Wrapper spawns AL LSP and proxies communication
