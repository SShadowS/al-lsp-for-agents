# AL Extension Auto-Download Design

Auto-download and update the Microsoft AL Language extension for environments without VS Code (Sublime Text, standalone usage). The wrapper handles downloading the .vsix from the VS Code Marketplace, extracting it, and keeping it updated.

## CLI Flags

Three new flags on `al-lsp-wrapper`:

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--auto-download-al-extension` | bool | `false` | Enable automatic download and daily update checks |
| `--al-extension-channel` | string | `release` | `release` or `prerelease` |
| `--force-update-al-extension` | bool | `false` | Bypass daily check, download latest now |

`--al-extension-channel` and `--force-update-al-extension` are only meaningful when `--auto-download-al-extension` is set. Ignored otherwise.

## Storage Layout

```
~/.al-language-server/
├── extensions/
│   ├── release/
│   │   ├── ms-dynamics-smb.al-18.0.2190758/   # extracted extension
│   │   │   └── bin/win32/Microsoft.Dynamics.Nav.EditorServices.Host.exe
│   │   └── metadata.json
│   └── prerelease/
│       ├── ms-dynamics-smb.al-19.0.xxxxx/
│       └── metadata.json
└── cache/
    └── al-extension.vsix                       # temp download, deleted after extraction
```

### metadata.json

Per channel:

```json
{
  "version": "18.0.2190758",
  "channel": "release",
  "lastCheckTime": "2026-04-07T10:30:00Z",
  "downloadedAt": "2026-04-06T14:22:00Z",
  "extensionDir": "ms-dynamics-smb.al-18.0.2190758"
}
```

- `lastCheckTime` drives the daily check interval
- On update, old extension directory is deleted after the new one is extracted successfully
- `cache/` is cleaned up after extraction (or on next startup if a crash left it behind)

## Path Resolution Chain

Updated priority order in `paths.go`:

1. `--al-extension-path` flag (explicit override, unchanged)
2. `AL_EXTENSION_PATH` env var (unchanged)
3. Auto-discover from VS Code/Cursor/etc. dirs (unchanged)
4. **New:** `~/.al-language-server/extensions/{channel}/` (only when `--auto-download-al-extension`)

If tiers 1-3 all miss and `--auto-download-al-extension` is not set, fail with error:

> AL Language extension not found. Install it in VS Code, set --al-extension-path, or use --auto-download-al-extension to download it automatically.

If a VS Code installation exists (tier 3), it wins even with `--auto-download-al-extension` set.

## Startup Flow

```
Startup
  ├─ Resolve path (tiers 1-3)
  ├─ Found? → use it, done
  ├─ --auto-download-al-extension not set? → fail with error
  └─ Tier 4: check ~/.al-language-server/extensions/{channel}/
      ├─ No local copy (first run)
      │   → blocking download, extract, start
      ├─ Local copy exists, --force-update set
      │   → start with existing, download in background, log when ready
      ├─ Local copy exists, check due (>24h)
      │   → start with existing, check + download in background if newer
      └─ Local copy exists, check not due
          → use existing, no network activity
```

The only blocking download is the very first run when nothing exists. All other updates happen in the background and take effect on next startup.

## Download & Update Logic

### First run (no local copy)

1. Query VS Code Marketplace API for latest version in selected channel
2. Download .vsix to `~/.al-language-server/cache/`
3. Extract to `~/.al-language-server/extensions/{channel}/`
4. Write `metadata.json`
5. Clean up cache
6. Continue startup

### Subsequent startups

1. Read `metadata.json` for active channel
2. If `--force-update-al-extension`: skip to step 4
3. If `lastCheckTime` < 24 hours ago: use existing, done
4. Query marketplace API for latest version (in background)
5. If version matches: update `lastCheckTime`, done
6. If newer: download, extract to new dir, delete old dir, update metadata
7. Updated version takes effect on next startup

### Error handling

- Network failure on first run: fail with clear error
- Network failure on update check: log warning, continue with existing version
- Corrupt/partial download: delete and retry once, then fail
- Extraction failure: same as corrupt download

## VS Code Marketplace API

Query endpoint:

```
POST https://marketplace.visualstudio.com/_apis/public/gallery/extensionquery
```

Request body queries for `ms-dynamics-smb.al` with flags to include version info and prerelease status. Response includes version entries with properties; prerelease versions have `Microsoft.VisualStudio.Code.PreRelease` property set to `"true"`.

Download URL:

```
https://marketplace.visualstudio.com/_apis/public/gallery/publishers/ms-dynamics-smb/vsextensions/al/{version}/vspackage
```

No external Go dependencies needed - standard library covers HTTP and zip extraction.

## Sublime Text Integration

### Wrapper launch

`plugin.py` passes the new flags when starting the wrapper:

```python
["al-lsp-wrapper", "--auto-download-al-extension", "--al-extension-channel", channel]
```

### Settings

Add `al_extension_channel` to `LSP-AL.sublime-settings` (default: `"release"`).

### Command palette entries

- **LSP-AL: Switch to Pre-release Channel** / **Switch to Release Channel** - updates setting, restarts server
- **LSP-AL: Force Update AL Extension** - restarts server with `--force-update-al-extension`

## Implementation Scope

### Go wrapper (`al-language-server-go/`)

- New file: `wrapper/extension_download.go` - marketplace API client, download, extraction
- Modified: `main.go` - new CLI flags
- Modified: `wrapper/paths.go` - tier 4 in resolution chain, metadata reading
- Modified: `wrapper/wrapper.go` - background update goroutine on startup

### Sublime package (`sublime-lsp-al/`)

- Modified: `plugin.py` - pass new flags, add commands
- Modified: `LSP-AL.sublime-settings` - add channel setting
