# Contributing to AL LSP for Agents

## Project Overview

This repo produces **one product** (the AL LSP wrapper) in **three distribution formats**:

```
Source (one)  →  Distributions (three)
                 ├── Claude Code plugin  (plugin.json + .lsp.json + bins)
al-language-     ├── VS Code extension   (vscode-extension/ + bins)
server-go/       └── Standalone binary   (GitHub release assets)
```

## Repository Structure

| Directory | Purpose |
|-----------|---------|
| `al-language-server-go/` | Go wrapper source code — **all wrapper logic lives here** |
| `al-language-server-go-windows/` | Claude Code plugin packaging (Windows) |
| `al-language-server-go-linux/` | Claude Code plugin packaging (Linux) |
| `vscode-extension/` | VS Code extension (Language Model Tools for Copilot) |
| `test-al-project/` | Integration tests |
| `.claude-plugin/` | Claude Code marketplace manifest |
| `.github/workflows/` | CI/CD pipelines |
| `docs/` | Design specs, implementation plans, and standalone usage guide |

## Key Rules

1. **All wrapper logic** goes in `al-language-server-go/wrapper/*.go` — never in distribution directories
2. **Distribution directories** are packaging only — they contain config files and binaries
3. **Binaries are build artifacts** — do NOT commit them. CI builds them, or run `./build.sh` locally
4. **Versions stay in sync** across all plugin.json, package.json, and marketplace.json files

## Development Setup

### Prerequisites

- Go 1.21+
- Node.js 20+ (for VS Code extension)
- Python 3 (for integration tests)
- The MS AL extension installed in VS Code

### Building Locally

```bash
# Build everything (requires al-call-hierarchy and tree-sitter-al repos next to this one)
./build.sh

# Build only Go binaries
./build.sh --skip-rust

# Build only Rust binaries (al-call-hierarchy)
./build.sh --skip-go
```

### Testing

```bash
# Go unit tests
cd al-language-server-go && go test ./... -v

# Integration tests (requires AL Language Server installed)
cd test-al-project && python test_lsp_go.py --wrapper go

# VS Code extension (compile check)
cd vscode-extension && npm run compile
```

### Local Testing by Distribution

**Claude Code:**
```powershell
# Use the dev marketplace (see CLAUDE.md for setup)
/plugin marketplace add ./.claude-plugin-dev/
/plugin install al-language-server-go-windows@claude-code-lsps-dev
```

**VS Code extension:**
```powershell
# Copy binaries into extension
cp al-language-server-go-windows/bin/* vscode-extension/bin/

# Launch VS Code with the extension
code --extensionDevelopmentPath=vscode-extension path/to/al-project
```

**Standalone:**
```bash
# Run the wrapper directly
./al-lsp-wrapper --al-extension-path /path/to/ms-dynamics-smb.al-17.x.x
```

## Pull Request Process

1. Branch from `main`, PR back to `main`
2. PRs must pass CI (Go build + tests, VS Code extension compile)
3. In the PR description, note which distribution(s) are affected
4. If touching Go wrapper code: run `python test_lsp_go.py --wrapper go` locally

## Release Process

1. Update versions in all files (see Versioning below)
2. Create and push a version tag: `git tag v1.5.0 && git push origin v1.5.0`
3. CI automatically:
   - Downloads `al-call-hierarchy` binaries from its latest release
   - Builds Go wrappers for Windows + Linux
   - Creates GitHub release with standalone binaries + `.vsix` files
   - Publishes to VS Code Marketplace

## Versioning

Update version in **all** of these files when releasing:

1. `al-language-server-go-windows/plugin.json`
2. `al-language-server-go-linux/plugin.json`
3. `.claude-plugin/marketplace.json`
4. `vscode-extension/package.json`

`al-call-hierarchy` has its own independent version — this repo downloads its latest release.

## Dependency Chain

```
tree-sitter-al          (build-time grammar, no releases needed)
       |
al-call-hierarchy       (Rust binary, own GitHub releases)
       |
al-lsp-for-agents       (Go binary + downloads al-call-hierarchy)
```
