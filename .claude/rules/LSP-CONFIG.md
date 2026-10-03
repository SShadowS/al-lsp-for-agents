# Claude Code LSP Configuration Reference

This document provides the complete reference for configuring Language Server Protocol (LSP) servers in Claude Code plugins.

## What LSP Provides

LSP integration gives Claude real-time code intelligence:

- **Instant diagnostics**: Claude sees errors and warnings immediately after each edit
- **Code navigation**: go to definition, find references, and hover information
- **Language awareness**: type information and documentation for code symbols

## Configuration Location

LSP can be configured in two ways:

1. **Standalone file**: `.lsp.json` in plugin root
2. **Inline**: `lspServers` field in `plugin.json`

## Configuration Formats

### .lsp.json File Format

```json
{
  "go": {
    "command": "gopls",
    "args": ["serve"],
    "extensionToLanguage": {
      ".go": "go"
    }
  }
}
```

### Inline in plugin.json

```json
{
  "name": "my-plugin",
  "lspServers": {
    "go": {
      "command": "gopls",
      "args": ["serve"],
      "extensionToLanguage": {
        ".go": "go"
      }
    }
  }
}
```

## Configuration Fields

### Required Fields

| Field | Description |
|-------|-------------|
| `command` | The LSP binary to execute (must be in PATH) |
| `extensionToLanguage` | Maps file extensions to language identifiers |

### Optional Fields

| Field | Description |
|-------|-------------|
| `args` | Command-line arguments for the LSP server |
| `transport` | Communication transport: `stdio` (default) or `socket` |
| `env` | Environment variables to set when starting the server |
| `initializationOptions` | Options passed to the server during initialization |
| `settings` | Settings passed via workspace/didChangeConfiguration |
| `workspaceFolder` | Workspace folder path for the server |
| `startupTimeout` | Max time to wait for server startup (milliseconds) |
| `shutdownTimeout` | Max time to wait for graceful shutdown (milliseconds) |
| `restartOnCrash` | Whether to automatically restart the server if it crashes |
| `maxRestarts` | Maximum number of restart attempts before giving up |

### Debug Logging

To enable verbose logging (activated via `--enable-lsp-logging`):

```json
{
  "language-id": {
    "command": "lsp-server",
    "loggingConfig": {
      "args": ["--log-level", "4"],
      "env": {
        "LSP_LOG": "-level verbose -file ${CLAUDE_PLUGIN_LSP_LOG_FILE}"
      }
    }
  }
}
```

Logs are written to `~/.claude/debug/`.

## Available LSP Plugins

These plugins are available in the Claude Code marketplace:

| Plugin | Language Server | Install Command |
|--------|-----------------|-----------------|
| pyright-lsp | Pyright (Python) | `pip install pyright` or `npm install -g pyright` |
| typescript-lsp | TypeScript Language Server | `npm install -g typescript-language-server typescript` |
| rust-lsp | rust-analyzer | See [rust-analyzer installation](https://rust-analyzer.github.io/manual.html#installation) |

**Important**: Install the language server first, then install the plugin from the marketplace.

## Common Language Server Configurations

### Python (Pyright)
```json
{
  "python": {
    "command": "pyright-langserver",
    "args": ["--stdio"],
    "extensionToLanguage": {
      ".py": "python",
      ".pyi": "python"
    }
  }
}
```

### TypeScript/JavaScript
```json
{
  "typescript": {
    "command": "typescript-language-server",
    "args": ["--stdio"],
    "extensionToLanguage": {
      ".ts": "typescript",
      ".tsx": "typescriptreact",
      ".js": "javascript",
      ".jsx": "javascriptreact",
      ".mts": "typescript",
      ".cts": "typescript",
      ".mjs": "javascript",
      ".cjs": "javascript"
    }
  }
}
```

### Go
```json
{
  "go": {
    "command": "gopls",
    "args": ["serve"],
    "extensionToLanguage": {
      ".go": "go"
    }
  }
}
```

### Rust
```json
{
  "rust": {
    "command": "rust-analyzer",
    "extensionToLanguage": {
      ".rs": "rust"
    }
  }
}
```

### C/C++ (clangd)
```json
{
  "c": {
    "command": "clangd",
    "extensionToLanguage": {
      ".c": "c",
      ".cpp": "cpp",
      ".cc": "cpp",
      ".cxx": "cpp",
      ".h": "c",
      ".hpp": "cpp",
      ".hxx": "cpp"
    }
  }
}
```

### Java (jdtls)
```json
{
  "java": {
    "command": "jdtls",
    "extensionToLanguage": {
      ".java": "java"
    },
    "startupTimeout": 30000
  }
}
```

### PHP
```json
{
  "php": {
    "command": "phpactor",
    "args": ["language-server"],
    "extensionToLanguage": {
      ".php": "php"
    }
  }
}
```

### Ruby
```json
{
  "ruby": {
    "command": "solargraph",
    "args": ["stdio"],
    "extensionToLanguage": {
      ".rb": "ruby",
      ".rake": "ruby",
      ".gemspec": "ruby"
    }
  }
}
```

### Lua
```json
{
  "lua": {
    "command": "lua-language-server",
    "extensionToLanguage": {
      ".lua": "lua"
    }
  }
}
```

### Bash
```json
{
  "bash": {
    "command": "bash-language-server",
    "args": ["start"],
    "extensionToLanguage": {
      ".sh": "shellscript",
      ".bash": "shellscript"
    }
  }
}
```

### YAML
```json
{
  "yaml": {
    "command": "yaml-language-server",
    "args": ["--stdio"],
    "extensionToLanguage": {
      ".yaml": "yaml",
      ".yml": "yaml"
    }
  }
}
```

### Terraform
```json
{
  "terraform": {
    "command": "terraform-ls",
    "args": ["serve"],
    "extensionToLanguage": {
      ".tf": "terraform",
      ".tfvars": "terraform"
    }
  }
}
```

### Zig
```json
{
  "zig": {
    "command": "zls",
    "extensionToLanguage": {
      ".zig": "zig"
    }
  }
}
```

### Dart
```json
{
  "dart": {
    "command": "dart",
    "args": ["language-server"],
    "extensionToLanguage": {
      ".dart": "dart"
    }
  }
}
```

### Kotlin
```json
{
  "kotlin": {
    "command": "kotlin-language-server",
    "extensionToLanguage": {
      ".kt": "kotlin",
      ".kts": "kotlin"
    }
  }
}
```

### Swift
```json
{
  "swift": {
    "command": "sourcekit-lsp",
    "extensionToLanguage": {
      ".swift": "swift"
    }
  }
}
```

### Elixir
```json
{
  "elixir": {
    "command": "elixir-ls",
    "extensionToLanguage": {
      ".ex": "elixir",
      ".exs": "elixir"
    }
  }
}
```

## Plugin Structure

A complete LSP plugin structure:

```
my-lsp-plugin/
├── plugin.json         # Plugin metadata
├── .lsp.json           # LSP configuration (or inline in plugin.json)
└── README.md           # Documentation (optional)
```

### plugin.json Example

```json
{
  "name": "my-lsp-plugin",
  "version": "1.0.0",
  "description": "LSP plugin for My Language",
  "author": {
    "name": "Your Name"
  },
  "license": "MIT",
  "keywords": ["lsp", "my-language"]
}
```

## Troubleshooting

### "Executable not found in $PATH"

You must install the language server binary separately. LSP plugins configure how Claude Code connects to a language server, but they don't include the server itself.

Check the `/plugin` Errors tab for this error and install the required binary for your language.

### "No LSP server available" Error
1. Verify the language server binary is installed and in PATH
2. Check if the plugin is enabled in `~/.claude/settings.json`
3. Ensure file extension matches `extensionToLanguage` mapping
4. Run with `--enable-lsp-logging` to see detailed errors
5. Upgrade to Claude Code v2.1.0+ (earlier versions have a race condition bug)

### Server Crashes Repeatedly
1. Increase `maxRestarts` if the server is unstable
2. Check server logs in `~/.claude/debug/`
3. Verify `initializationOptions` are correct for your server

### Server Starts but Features Don't Work
1. Ensure the server supports the LSP operations you're using
2. Check if `settings` need to be configured
3. Some servers require project-specific config files (e.g., `tsconfig.json`, `pyrightconfig.json`)

### Slow Server Startup
1. Increase `startupTimeout` for servers like jdtls that take longer to initialize
2. Default timeout is typically 5000ms (5 seconds)

## Plugin Registration

Plugins must be registered in `~/.claude/plugins/installed_plugins.json` and enabled in `~/.claude/settings.json`:

```json
{
  "enabledPlugins": {
    "my-lsp-plugin@source": true
  }
}
```
