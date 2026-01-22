# Plan: AL MCP Server - LSP Features as MCP Tools

## Overview

Embed an MCP server directly into the existing AL LSP wrappers. The wrapper will serve:
- **LSP over stdio** → Claude Code's LSP integration (existing)
- **MCP over HTTP** → Claude Code's MCP integration (new)

This gives MCP tools direct access to LSP state (cached diagnostics, open files, project state).

## Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│                        Claude Code                               │
│                                                                  │
│   LSP Client ◄──stdio──┐          ┌──stdio──► MCP Client        │
└────────────────────────┼──────────┼──────────────────────────────┘
                         │          │
                         ▼          ▼
              ┌──────────────┐  ┌─────────────┐
              │al-lsp-wrapper│  │   al-mcp    │
              │   (LSP)      │  │   (shim)    │
              └──────┬───────┘  └──────┬──────┘
                     │                 │
                     │    HTTP API     │
                     │◄────────────────┘
                     │
        ┌────────────┴────────────┐
        │     al-lsp-wrapper      │
        │  ┌─────────┐ ┌────────┐ │
        │  │LSP Proxy│ │HTTP API│ │
        │  └────┬────┘ └────┬───┘ │
        │       │           │     │
        │  ┌────▼───────────▼───┐ │
        │  │  Diagnostic Cache  │ │
        │  └─────────┬──────────┘ │
        │            │            │
        │     ┌──────▼──────┐     │
        │     │  AL Language │    │
        │     │    Server    │    │
        │     └──────────────┘    │
        └─────────────────────────┘

Claude Code connects via:
  - stdio to al-lsp-wrapper for LSP operations
  - stdio to al-mcp shim, which calls wrapper's HTTP API for MCP tools
```

## MCP Tools to Implement

### Phase 1: Diagnostics (Core)
| Tool | Description |
|------|-------------|
| `get_diagnostics` | Get errors/warnings for a file or entire project |
| `get_diagnostic_summary` | Quick overview: X errors, Y warnings per file |

### Phase 2: Code Intelligence
| Tool | Description |
|------|-------------|
| `get_completions` | Get completion suggestions at a position |
| `get_signature_help` | Get function signature at cursor |
| `get_code_actions` | Get available fixes for a diagnostic or range |
| `apply_code_action` | Apply a specific code action |

### Phase 3: Formatting & Refactoring
| Tool | Description |
|------|-------------|
| `format_document` | Format entire file |
| `format_range` | Format selection |
| `rename_symbol` | Rename symbol across project |

### Phase 4: Advanced
| Tool | Description |
|------|-------------|
| `get_semantic_tokens` | Get semantic highlighting info |
| `get_folding_ranges` | Get code folding regions |

## Implementation Steps

### Step 1: Add HTTP API Server to Go Wrapper

**File:** `al-language-server-go/wrapper/http_api.go` (new)

```go
type HTTPAPI struct {
    wrapper *ALLSPWrapper
    port    int
    server  *http.Server
}

// Endpoints:
// GET  /health              - Health check
// GET  /diagnostics         - Get all cached diagnostics
// GET  /diagnostics?file=X  - Get diagnostics for file
// POST /lsp/completion      - Get completions at position
// POST /lsp/codeAction      - Get code actions
// POST /lsp/format          - Format document
// POST /lsp/rename          - Rename symbol
```

- Start on port 0 (auto-assign), write port to `.claude/al-mcp.port`
- JSON API, not MCP protocol (simpler, MCP shim handles protocol)

### Step 2: Add Diagnostic Cache

**File:** `al-language-server-go/wrapper/diagnostic_cache.go` (new)

```go
type DiagnosticCache struct {
    mu          sync.RWMutex
    diagnostics map[string][]Diagnostic // URI -> diagnostics
    updated     map[string]time.Time
}

func (c *DiagnosticCache) Update(uri string, diags []Diagnostic)
func (c *DiagnosticCache) Get(uri string) []Diagnostic
func (c *DiagnosticCache) GetAll() map[string][]Diagnostic
```

- Intercept `textDocument/publishDiagnostics` in notification handler
- Store diagnostics by file URI with timestamps

### Step 3: Update Wrapper to Start HTTP API

**File:** `al-language-server-go/wrapper/wrapper.go` (modify)

- Add `httpAPI *HTTPAPI` field
- Start HTTP server in `Run()` after AL LSP starts
- Hook diagnostic cache into notification forwarding

### Step 4: Create MCP Shim Binary

**File:** `al-language-server-go/cmd/mcp/main.go` (new)

```go
func main() {
    // 1. Find port file in workspace
    port := readPortFile(".claude/al-mcp.port")

    // 2. Create MCP server over stdio
    server := mcp.NewServer(os.Stdin, os.Stdout)

    // 3. Register tools that call HTTP API
    server.RegisterTool("get_diagnostics", ...)
    server.RegisterTool("get_completions", ...)

    // 4. Run
    server.Run()
}
```

### Step 5: Implement MCP Protocol

**File:** `al-language-server-go/mcp/server.go` (new)

Minimal MCP protocol implementation:
- Handle `initialize`, `tools/list`, `tools/call`
- JSON-RPC over stdio
- No external SDK dependency

**File:** `al-language-server-go/mcp/tools.go` (new)

```go
var Tools = []Tool{
    {
        Name: "get_diagnostics",
        Description: "Get compiler errors and warnings for AL files",
        InputSchema: Schema{
            Type: "object",
            Properties: map[string]Property{
                "file": {Type: "string", Description: "File path (optional, all if omitted)"},
            },
        },
    },
    // ... more tools
}
```

### Step 6: Create MCP Plugin Directory

**New directories:** `al-mcp-server-go-windows/` (and linux/darwin variants)

```
al-mcp-server-go-windows/
├── plugin.json
├── .mcp.json
└── bin/
    └── al-mcp.exe
```

**`plugin.json`:**
```json
{
  "name": "al-mcp-server-go-windows",
  "version": "1.0.0",
  "description": "MCP tools for AL Language Server (diagnostics, code actions, etc.)",
  "author": { "name": "SShadowS" },
  "repository": "https://github.com/SShadowS/claude-code-lsps",
  "license": "MIT",
  "keywords": ["al", "dynamics", "business-central", "mcp", "diagnostics"]
}
```

**`.mcp.json`:**
```json
{
  "mcpServers": {
    "al-tools": {
      "type": "stdio",
      "command": "${CLAUDE_PLUGIN_ROOT}/bin/al-mcp.exe",
      "args": []
    }
  }
}
```

### Step 7: Implement Same in Python Wrapper

**Files to modify/add:**
- `al-language-server-python/al_lsp_wrapper.py` - Add HTTP API server
- `al-language-server-python/al_mcp.py` (new) - MCP shim in Python

### Step 8: Update Build Script

**File:** `build.sh` (modify)

- Ensure MCP dependencies are included
- Build platform binaries as before

## Port Discovery Strategy

Since HTTP port can't be embedded in static config, options:

**Option A: Fixed Port (Simple)**
- Use port 47100 (or configurable via env var)
- Risk: port conflicts
- `.mcp.json` uses: `"url": "http://localhost:47100"`

**Option B: MCP Shim Binary (Recommended)**
- Wrapper starts HTTP API server, writes port to file
- Separate lightweight `al-mcp` binary:
  1. Reads port from `.claude/al-mcp.port`
  2. Speaks MCP protocol over stdio to Claude Code
  3. Translates MCP calls to HTTP API calls to wrapper
- `.mcp.json` uses stdio transport with `al-mcp` binary
- No port hardcoding in config!

```
Claude Code ──stdio──▶ al-mcp ──HTTP──▶ al-lsp-wrapper ──▶ AL LSP
   (MCP)                (shim)           (HTTP API)
```

**Option C: Combined stdio (Complex)**
- Single binary handles both LSP and MCP over separate stdio channels
- Very complex, not recommended

**Recommended: Option B (MCP Shim)**
- Clean separation of concerns
- Port discovery is internal implementation detail
- `.mcp.json` just specifies command, no URLs

## Files to Create/Modify

### New Files
| File | Purpose |
|------|---------|
| `al-language-server-go/wrapper/http_api.go` | HTTP API server for internal use |
| `al-language-server-go/wrapper/diagnostic_cache.go` | Diagnostic caching |
| `al-language-server-go/cmd/mcp/main.go` | MCP shim binary entry point |
| `al-language-server-go/mcp/server.go` | MCP protocol implementation |
| `al-language-server-go/mcp/tools.go` | MCP tool definitions |
| `al-mcp-server-go-windows/plugin.json` | MCP plugin manifest |
| `al-mcp-server-go-windows/.mcp.json` | MCP server config (stdio) |
| `al-mcp-server-go-windows/bin/al-mcp.exe` | MCP shim binary |
| (same for linux/darwin variants) | |

### Modified Files
| File | Changes |
|------|---------|
| `al-language-server-go/wrapper/wrapper.go` | Start MCP server, wire cache |
| `al-language-server-python/al_lsp_wrapper.py` | Add MCP server + tools |
| `build.sh` | Build MCP plugin variants |
| `.claude-plugin/marketplace.json` | Add MCP plugin entries |

## Verification

1. **Start wrapper with HTTP API:**
   ```bash
   cd test-al-project
   ../al-language-server-go-windows/bin/al-lsp-wrapper.exe
   # Check .claude/al-mcp.port was created
   cat .claude/al-mcp.port
   ```

2. **Test HTTP API directly:**
   ```bash
   PORT=$(cat .claude/al-mcp.port)
   curl http://localhost:$PORT/health
   curl http://localhost:$PORT/diagnostics
   ```

3. **Test MCP shim:**
   ```bash
   # Should respond to MCP initialize
   echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' | \
     ../al-mcp-server-go-windows/bin/al-mcp.exe
   ```

4. **Run existing LSP tests:**
   ```bash
   python test_lsp_go.py --wrapper both
   ```

5. **Create MCP integration test:**
   ```bash
   python test_mcp.py  # New test file
   ```

6. **Test MCP in Claude Code:**
   - Install both LSP plugin AND MCP plugin
   - Open an AL project with errors
   - Ask Claude: "What errors are in this file?"
   - Claude should use `get_diagnostics` tool

## Open Questions

1. **Workspace discovery:** How does MCP shim find the right `.claude/al-mcp.port` file when multiple workspaces are open?
   - Option: Search up from CWD
   - Option: Pass workspace as environment variable

2. **Wrapper lifecycle:** What happens if MCP shim starts before wrapper?
   - Option: Retry with backoff
   - Option: Return "LSP not ready" error

## Dependencies

- Go: `net/http` (stdlib)
- Python: `http.server` (stdlib)
- No external MCP SDK needed - implement minimal protocol

## Risks

1. **Port conflicts:** Fixed port may clash with other services
2. **Timing:** MCP server must be ready before Claude Code tries to connect
3. **State sync:** Diagnostics cached in wrapper must stay fresh
