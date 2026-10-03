# CLAUDE.md

This file provides guidance to Claude Code when working with this repository.

## User Environment

- **Shell**: User runs PowerShell, not bash. When providing commands for the user to run manually, use PowerShell syntax.
- **Platform**: Windows

## Project Overview

AL LSP for Agents — AL Language Server wrappers for AI-powered Business Central development. Ships as:

- **VS Code extension** with Language Model Tools for GitHub Copilot agent mode
- **Claude Code plugin** via the marketplace (Windows and Linux)
- **Standalone binaries** for integration with other tools

The Go wrapper sits between the editor and the Microsoft AL Language Server, adding capabilities from [al-sem](https://github.com/SShadowS/al-sem) (a tree-sitter-based analysis server).

## Repository Structure

```
.claude-plugin/marketplace.json          # Claude Code marketplace manifest
al-language-server-go/                   # Go wrapper source (shared)
al-language-server-go-windows/           # Windows plugin (binaries + config)
al-language-server-go-linux/             # Linux plugin (binaries + config)
vscode-extension/                        # VS Code extension (TypeScript)
test-al-project/                         # Test project and test scripts
docs/                                    # Specs and plans
.claude/rules/AL-LSP-RULES.md           # AL protocol rules
.claude-plugin-dev/.claude-plugin/       # Dev marketplace (gitignored)
```

## Building

### al-sem (sibling engine) gotchas

- The bulk of the unit tests live in the **lib**; the LSP server tests live in
  the `al-call-hierarchy` **bin** (`src/main.rs`). Run both:
  `cargo test --lib --bin al-call-hierarchy`. (There is no bin named `al-sem`;
  the shipped bins are `al-call-hierarchy` and `alsem`.)
- Differential tests compare against **al-sem** TS-reference goldens at
  `U:\Git\al-sem` (or `$AL_SEM_DIR`); they're dev-only and skip when al-sem is
  absent. Simulate the CI gate locally with `AL_SEM_DIR=/nonexistent cargo test --all-targets`.
- Windows: `cargo test --all-targets` hits a PDB `LNK1318` linker flake building
  many integration test binaries at once; CI (Linux) is fine. Run single `--test <name>` locally.
- Dump a real AST with `tree-sitter parse <file.al>` from the `tree-sitter-al/` dir
  (standalone binary; `npx tree-sitter` is not installed).

### Build Script

```bash
# Build everything (requires al-sem and tree-sitter-al repos next to this one)
./build.sh

# Build only Rust binaries (al-sem)
./build.sh --skip-go

# Build only Go binaries
./build.sh --skip-rust
```

Builds two binaries per platform:
- **al-sem**: Call hierarchy LSP server (Rust)
- **al-lsp-wrapper**: Main wrapper executable (Go)

**Required sibling repositories:**
- `U:\Git\al-call-hierarchy` — al-sem engine source, which builds the `al-call-hierarchy`
  LSP binary this wrapper spawns. The directory keeps the old name; the GitHub repo and the
  crate were renamed to `al-sem` on 2026-08-07, and the binary deliberately was not
- `U:\Git\tree-sitter-al` — AL grammar for tree-sitter (build-time dependency)

For cross-compiling Rust to Linux: `cargo install cross` (requires Docker with Linux containers)

### Manual Go Builds

```bash
cd al-language-server-go

# Windows
go build -trimpath -ldflags="-s -w" -o ../al-language-server-go-windows/bin/al-lsp-wrapper.exe .

# Linux
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o ../al-language-server-go-linux/bin/al-lsp-wrapper .
```

### VS Code Extension

```bash
cd vscode-extension
npm install
npm run compile    # TypeScript compilation
npm run package    # Build .vsix
npm run publish    # Publish to VS Code Marketplace (needs VSCE_PAT)
```

### Checklist for Any Change

1. Implement in Go wrapper (`al-language-server-go/wrapper/*.go`)
2. Rebuild platform binaries (Windows + Linux)
3. The tracked dev/test binary is rebuilt automatically by `test_lsp_go.py`
   (it used to go stale silently and the suite would then pass against old
   code). To build it by hand:
   `cd al-language-server-go && go build -trimpath -ldflags="-s -w" -o bin/al-lsp-wrapper.exe .`
4. Run tests: `cd test-al-project && python test_lsp_go.py --wrapper go`
   (the harness rebuilds the wrapper from source first, so it can no longer
   test a stale binary; `--no-build` opts out)
5. If VS Code extension changed: `cd vscode-extension && npm run compile`
6. **Before tagging a release**, run `./scripts/preflight-release.ps1` — it
   fails if any *shipped* Windows binary is unsigned or the four version files
   disagree. Note `build.sh` no longer overwrites the deployed
   `al-call-hierarchy` binaries (al-sem's deploy bot owns those, and its copies
   are CI-signed); pass `--replace-engine` when you deliberately want a local
   engine build in the plugin dirs.
7. **Before committing rebuilt Windows binaries**, sign them:
   `./scripts/sign-binaries.ps1` (PowerShell; needs `Install-Module TrustedSigning` once, `az login`, and the `AZURE_SIGNING_*` env vars — see Code Signing below). The committed plugin binaries ship to Claude Code marketplace users directly from the repo, so an unsigned commit ships an unsigned exe.

## Wrapper Capabilities

### From Microsoft AL LSP
- Go to definition (`al/gotodefinition`)
- Hover information
- Document symbols / workspace symbols
- Diagnostics (compiler errors, warnings, deprecations)

### From al-sem Server
- **Call Hierarchy** — incoming/outgoing calls for procedures
- **Code Lens** — reference counts above procedures
- **Enriched Hover** — field and action properties extracted via tree-sitter (all declared properties, not a curated list)
- **Diagnostics** — code quality warnings (configurable via `.al-sem.json`, or the
  pre-rename `.al-call-hierarchy.json`, which is still read when the new file is absent):
  - Unused procedures (no callers)
  - High cyclomatic complexity (default: ≥5 warning, ≥10 critical)
  - Too many parameters (default: ≥4 warning, ≥7 critical)
  - High fan-in (default: >20 callers)
  - Long methods (default: >50 lines)

### From Microsoft almcp (MCP server)

The wrapper spawns Microsoft's `almcp` MCP server (preferring the nuget `al`
tool at `~/.dotnet/tools`, falling back to the AL extension's bundled `almcp`)
to back two custom LSP methods:

- **`al/symbolRelations`** — outgoing/incoming symbol relations (SourceTable,
  Extends, Implements, ExtendedBy, ...), dependency-scope.
- **`al/inspectPage`** — page control tree or action tree, including
  dependency pages. Requires the nuget `al` tool; the bundled almcp lacks this
  tool, so the wrapper returns an actionable install hint when it is absent.

Exposed in VS Code as `bclsp_symbolRelations` and `bclsp_inspectPage`.

### Document Event Forwarding

Document events (`didOpen`, `didClose`, `didChange`) are forwarded to both servers. The wrapper merges capabilities and adds `codeLensProvider` to the initialize response.

### Environment Variables

- `AL_LSP_ALT_EXT_DIR` — when set, overrides AL extension auto-discovery
  and points the inner Microsoft AL LS at this directory. Used by the
  matrix harness's `cell-isolated-cache` experiment to test the
  shared-cache hypothesis behind issue #17. Lower priority than the
  explicit `--al-extension-path` flag, higher than auto-discovery.
- `AL_LSP_PACKAGE_CACHE` — extra `.app` package folders (OS path-list
  separated) appended to every project's `packageCachePaths`, after the
  project's own and ancestor `.alpackages`. For containers that have no
  `.alpackages` but a shared symbol cache (DevOpsWorker review containers).
  Missing folders are skipped. Unset = no change.
- `AL_LSP_SOURCE_ROOTS` — folders scanned for AL projects; dependencies they
  satisfy become VS Code-style project references (closure + per-(reference,
  parent) `didChangeConfiguration` after `al/activeProjectLoaded`). Code in
  `wrapper/source_refs.go`, e2e test `test-al-project/test_source_refs.py`.
  Unset = no change. Design notes: `docs/spikes/2026-09-23-source-project-references.md`.

## AL LSP Protocol Notes

The AL Language Server uses custom commands beyond standard LSP:

- `al/gotodefinition` instead of `textDocument/definition`
- `al/setActiveWorkspace` for workspace initialization
- Files must be opened with `textDocument/didOpen` before operations work

See `.claude/rules/AL-LSP-RULES.md` for full protocol documentation.

## LSP Configuration

The `.lsp.json` file configures how Claude Code launches the language server.

**Key**: Use `${CLAUDE_PLUGIN_ROOT}` for the plugin install directory. `${pluginDir}` does not exist.

Dev marketplace paths must start with `./`:
- ✅ `"source": "./../../al-language-server-go-windows"`
- ❌ `"source": "../../al-language-server-go-windows"` (invalid)

## Release Process

There are 4 independently versioned components with a strict release order due to dependencies:

```
tree-sitter-al (grammar)
    ↓ build-time dependency
al-sem (Rust binary, version in Cargo.toml)
    ↓ downloaded from GitHub releases by CI
al-lsp-for-agents (Go wrapper + plugins + VS Code extension)
    ├── Claude Code plugins (plugin.json + marketplace.json)
    └── VS Code extension (package.json, published to Marketplace)
```

### Dependency Chain

- **al-sem CI** checks out `tree-sitter-al` from GitHub at build time
  **on its default branch, UNPINNED** — the released binary's grammar is whatever
  `tree-sitter-al` `main` HEAD is at CI time, NOT the al-sem submodule
  pin or vendored copy. A grammar bump can silently break al-sem; its
  `release.yml` now runs the full test suite as a gate so a broken build can't ship.
- **al-lsp-for-agents CI** downloads `al-sem` binaries from its **latest GitHub release**
- Therefore: **al-sem must be released first** and its CI must complete before tagging al-lsp-for-agents

### Step-by-Step Release

#### 1. tree-sitter-al (only if grammar changed)

```bash
cd U:\Git\tree-sitter-al
# Bump version in package.json, Cargo.toml
# Regenerate: tree-sitter generate
# Push and tag
git push && git push origin v<version>
```

#### 2. al-sem

```bash
cd U:\Git\al-call-hierarchy
```

**Version file:** `Cargo.toml` → `version = "X.Y.Z"`

```bash
# Bump version
# Edit Cargo.toml version field

# Commit and push
git add Cargo.toml
git commit -m "Bump version to X.Y.Z"
git push

# Tag to trigger release workflow
git tag -a vX.Y.Z -m "vX.Y.Z - description"
git push origin vX.Y.Z
```

**Wait for CI to complete:** https://github.com/SShadowS/al-sem/actions

The release workflow builds Windows + Linux binaries and creates a GitHub release with assets:
- `al-call-hierarchy-windows-x64.exe`
- `al-call-hierarchy-linux-x64`

#### 3. al-lsp-for-agents (Claude Code plugins + VS Code extension)

**IMPORTANT:** Only proceed after al-sem release is published (step 2 CI must be green).

```bash
cd U:\Git\claude-code-lsps
```

**Version files (all must match):**

| File | Field |
|------|-------|
| `al-language-server-go-windows/plugin.json` | `version` |
| `al-language-server-go-linux/plugin.json` | `version` |
| `.claude-plugin/marketplace.json` | `version` (2 entries, one per plugin) |
| `vscode-extension/package.json` | `version` |

**For local testing**, also copy updated binaries to Claude Code plugin dirs:

| Source | Destination |
|--------|-------------|
| `al-sem` Windows build | `al-language-server-go-windows/bin/al-call-hierarchy.exe` |
| `al-sem` Linux build | `al-language-server-go-linux/bin/al-call-hierarchy` |

These committed binaries are what Claude Code marketplace users get. The VS Code extension bins are gitignored — CI downloads them fresh from al-sem releases.

```bash
# Bump all 4 version files
# Commit and push
git add al-language-server-go-windows/plugin.json \
        al-language-server-go-linux/plugin.json \
        .claude-plugin/marketplace.json \
        vscode-extension/package.json
git commit -m "Bump all versions to X.Y.Z"
git push

# Tag to trigger release workflow
git tag -a vX.Y.Z -m "vX.Y.Z - description"
git push origin vX.Y.Z
```

**Release workflow does:**
1. Builds Go wrapper (Windows + Linux)
2. Downloads al-sem from its **latest GitHub release**
3. Signs the Windows binaries (Azure Artifact Signing, skipped until configured)
4. Packages standalone zips (Go wrapper + al-sem)
5. Packages VS Code extension .vsix (per platform, with binaries bundled)
6. Creates GitHub release with 4 assets
7. Publishes .vsix files to VS Code Marketplace (needs `VSCE_PAT` secret)

**Claude Code marketplace** is live immediately after push (reads from repo directly).

### Code Signing

Windows binaries are authenticode-signed with Azure Artifact Signing (Trusted
Signing). Three signing paths, one Azure account:

| Path | What it signs | Where |
|------|---------------|-------|
| al-sem CI (`release.yml` + `build-and-deploy.yml`) | `al-call-hierarchy.exe` in release assets AND the copies committed into this repo's plugin dirs | Upstream repo |
| This repo's `release.yml` | `al-lsp-wrapper.exe` (+ re-signs `al-call-hierarchy.exe`) in release zips and .vsix | Windows build job |
| `scripts/sign-binaries.ps1` | Committed `al-lsp-wrapper.exe` copies built locally | Local, before commit |

CI signing steps skip cleanly while unconfigured (gated on
`vars.AZURE_SIGNING_ACCOUNT != ''` — step-level `if`; job-level can't read
secrets). Both repos need identical config:

- **Repo variables:** `AZURE_SIGNING_ENDPOINT`, `AZURE_SIGNING_ACCOUNT`, `AZURE_SIGNING_PROFILE`
- **Repo secrets:** `AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, `AZURE_SUBSCRIPTION_ID` (OIDC federated credential, no client secret)
- The actual values, the owning Azure subscription and the Entra app id are
  in `CLAUDE.local.md` (gitignored). The signing account is NOT in the work
  tenant: `az login` must be on the personal account, or signing returns 403.
- Auth: `azure/login@v2` OIDC. Signing jobs carry `environment: release`
  because the federated credentials match subject
  `repo:SShadowS/<repo>:environment:release` — exact match only; flexible
  (wildcard tag) credentials are rejected by the tenant. Renaming or removing
  the `release` environment in either repo breaks signing.
- **OIDC subject format differs per repo** (GitHub's ID-qualified subject
  rollout): al-sem presents an ID-qualified subject (`repo:<owner>@<id>/al-sem@<id>:...`),
  al-lsp-for-agents still presents the classic `repo:SShadowS/al-lsp-for-agents:...`.
  The signing Entra app therefore carries BOTH credential forms; if GitHub
  later migrates this repo, the ID-qualified credential takes over. An
  AADSTS700213 error means the presented subject matches neither — compare
  `gh api repos/SShadowS/<repo>/actions/oidc/customization/sub` against
  `az ad app federated-credential list --id <app id from CLAUDE.local.md>`.
- Local script uses the same names as env vars (or `-Endpoint/-AccountName/-ProfileName`)
  + `az login` + `Install-Module TrustedSigning`; the signing user needs the
  "Artifact Signing Certificate Profile Signer" role on the account. It is a
  PowerShell script: from a Claude Code `!` prompt use `pwsh -File ./scripts/sign-binaries.ps1`

### Quick Reference: What Goes Where

| Binary | Claude Code Windows | Claude Code Linux | VS Code (CI only) |
|--------|--------------------|--------------------|-------------------|
| `al-sem` | `al-language-server-go-windows/bin/` (committed) | `al-language-server-go-linux/bin/` (committed) | Downloaded from GH release |
| `al-lsp-wrapper` | `al-language-server-go-windows/bin/` (committed) | `al-language-server-go-linux/bin/` (committed) | Built by CI |

## Testing

```bash
cd test-al-project
python test_lsp_go.py --wrapper go             # Run LSP tests
python test_lsp_go.py --wrapper go --show-logs  # Show wrapper logs after
```

### Client Capabilities Baseline

`test-al-project/.snapshots/claude-code-client-capabilities.json` tracks Claude Code's LSP client capabilities. Check periodically after Claude Code updates:

```bash
cd test-al-project
bash check-capabilities.sh                    # Compare against baseline
bash check-capabilities.sh --update-baseline  # Update after verifying diffs
```

## Matrix Harness

`harness/` runs VS Code under controlled extension combinations to
reproduce multi-extension bugs (notably issue #17) and protect feature
parity during refactors.

```bash
cd harness
npm run refresh         # populate sha256 hashes in extensions.lock.json
npm run test:matrix     # run all cells
npm run test:cell -- cell-control --record  # rerecord one cell's baseline
```

CI runs the matrix on every PR that touches the wrapper, the VS Code
extension, or the harness itself. See `.github/workflows/harness.yml`.

When the Layer 1 refactor lands (wrapper stops spawning a second AL LS
in VS Code mode), `cell-all-three`'s baseline must be re-recorded — the
"already declared" errors should disappear (when reproducible) and the
test enforces non-regression from that point forward.

## Dev Marketplace (Testing)

A dev marketplace (`.claude-plugin-dev/`) exists for testing config changes without affecting production (gitignored).

```powershell
# One-time setup (from repo root)
/plugin marketplace add ./                       # Production: al-lsp-for-agents
/plugin marketplace add ./.claude-plugin-dev/    # Development: al-lsp-for-agents-dev

# Install from dev marketplace
/plugin install al-language-server-go-windows@al-lsp-for-agents-dev
```

## Reference Material

The VS Code AL extension is available locally for reference:
`c:\Users\SShadowS\.vscode\extensions\ms-dynamics-smb.al-18.0.2190758`
