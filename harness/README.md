# AL LSP for Agents — VS Code Matrix Harness

Reproduces multi-extension diagnostic interactions deterministically.

## Quick start

    cd harness
    npm install
    npm run refresh        # populate extensions.lock.json sha256
    npm run test:unit      # fast helper unit tests
    npm run test:matrix    # full matrix (downloads vsix on first run)

## Layout

- `extensions.lock.json` — pinned extension versions + sha256
- `cells/*.ts` — one matrix cell per file
- `baseline/*.json` — expected diagnostics per cell
- `suite/` — tests that run inside VS Code via test-electron
- `unit/` — tests for harness helpers, run in plain Node

## Refreshing pinned versions

    npm run refresh -- --bump ms-dynamics-smb.al

Updates the lock file with the latest available version and recomputes sha256.
Commit the updated lock file.
