# AL LSP for Agents — VS Code Matrix Harness

Reproduces multi-extension diagnostic interactions deterministically.
Used to lock in feature parity before architectural changes to the wrapper.

## What it does

Runs VS Code via `@vscode/test-electron` once per matrix cell. Each cell
combines a different subset of AL extensions (Microsoft AL, AL LSP for
Agents, AL Test Runner) against the `test-al-project/` fixture and
captures the diagnostics that VS Code reports. Captured snapshots are
compared against committed baselines under `baseline/`.

## Quick start

    cd harness
    npm install
    npm run refresh        # populate extensions.lock.json sha256 (network)
    npm run test:unit      # fast helper unit tests
    npm run test:matrix    # full matrix (downloads vsix on first run)

## Cells

| Cell | Extensions | Purpose |
|---|---|---|
| `cell-control` | Microsoft AL only | Control baseline. |
| `cell-with-wrapper` | + AL LSP for Agents | Wrapper alone is clean. |
| `cell-with-test-runner` | + AL Test Runner | AL Test Runner alone is clean. |
| `cell-all-three` | All three | Reproduces issue #17. |
| `cell-isolated-cache` | All three + alt cache dir | Causal probe for shared-cache hypothesis. |

## Refreshing pinned versions

    npm run refresh                          # rehash all
    npm run refresh -- --bump <ext-id>       # bump one to latest

Commit the updated `extensions.lock.json`.

## Re-recording a baseline

When a fix or a fixture change is expected to alter diagnostics, re-record:

    npm run test:cell -- <cell-name> --record

Inspect the JSON, then commit. Recording does NOT verify the change is
intentional — review the diff before committing.

## Findings (issue #17)

All five cells in the current `test-al-project/` fixture produce empty
diagnostic snapshots. The fixture is too small to trigger the false
"already declared" diagnostics that DigiTecKid reported on a real BC
project. The harness machinery works (cells launch VS Code, install
extensions, capture diagnostics deterministically); the bug just doesn't
reproduce on this minimal fixture. Two paths forward:

1. Expand the fixture with more AL objects, multiple apps, and the
   parsing/analysis paths that DigiTecKid's project exercised.
2. Accept the harness as feature-parity protection rather than a bug
   reproducer, and rely on user reports for #17 verification.

The capability-parity contract test (`suite/parity.test.ts`) is present
but currently self-skips — `vscode.executeDefinitionProvider` returns
empty for AL files because the MS AL extension uses a custom
`al/gotodefinition` request, not standard LSP. Layer 1 must invoke
`al/gotodefinition` directly via the AL extension's LSP client.

## Known limitations

- AL extension cold-start can take 60+ seconds on a slow CI runner; the
  readiness state machine has a 120-second hard timeout. Increase if
  runs become flaky.
- The harness only opens files listed in `TARGET_FILES` inside
  `suite/diagnostics.test.ts`. Diagnostics for files not opened are not
  captured. Extend that array when fixture coverage expands.
- Microsoft AL extension licensing: the `.vsix` is downloaded from the
  public Marketplace at test time and cached locally. The cache is
  gitignored; nothing is redistributed.
