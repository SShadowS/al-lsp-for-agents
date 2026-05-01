# AL LSP for Agents — VS Code Matrix Harness

Reproduces multi-extension diagnostic interactions deterministically.
Used to lock in feature parity before architectural changes to the wrapper.

## What it does

Runs VS Code via `@vscode/test-electron` once per matrix cell. Each cell
combines a different subset of AL extensions (Microsoft AL, AL LSP for
Agents, AL Test Runner) against a fixture and captures the diagnostics
that VS Code reports. Captured snapshots are compared against committed
baselines under `baseline/`.

Cells can use the default `test-al-project/` fixture, a synthetic fixture
shipped under `harness/fixtures/`, or an external fixture path (used by
the `cell-real-*` cells which point at a real customer project sitting
outside this repo). Cells whose fixture isn't present on the current
machine are skipped automatically by `npm run test:matrix`.

## Quick start

    cd harness
    npm install
    npm run refresh        # populate extensions.lock.json sha256 (network)
    npm run test:unit      # fast helper unit tests
    npm run test:matrix    # full matrix (downloads vsix on first run)

## Cells

### Default fixture (`test-al-project/`)

| Cell | Extensions | Purpose |
|---|---|---|
| `cell-control` | Microsoft AL only | Control baseline. |
| `cell-with-wrapper` | + AL LSP for Agents | Wrapper alone behavior. |
| `cell-with-test-runner` | + AL Test Runner | AL Test Runner alone behavior. |
| `cell-all-three` | All three | Three-way interaction. |
| `cell-isolated-cache` | All three + `AL_LSP_ALT_EXT_DIR` | Causal probe for shared-cache hypothesis. |

### Synthetic multi-app fixtures (`harness/fixtures/`)

Each cell uses all three extensions but a different fixture shape. None
reproduce the issue #17 false-duplicate diagnostics on synthetic data:

| Cell | Fixture | Shape |
|---|---|---|
| `cell-all-three-fbemc` | `fbemc-shape` | Multi-root: source + dependent app via .app symbol |
| `cell-all-three-pkg-collision` | `pkg-collision` | Two .app files with same publisher/name/version, different ids |
| `cell-all-three-multi` | `multi-app-workspace` | Two unrelated AL apps multi-root |
| `cell-all-three-test-app` | `with-test-app` | Multi-root: main + test app depending on main |

### External-fixture cells (require `u:\Git\DO.Support-UISimply\`)

These cells point at a real customer BC project that lives outside this
repo. They are auto-skipped by `npm run test:matrix` when that path
doesn't exist. They DO deterministically reproduce issue #17.

| Cell | Extensions | Result |
|---|---|---|
| `cell-real-control` | Microsoft AL only | **AL0264 + AL0197 fire** |
| `cell-real-no-wrapper` | + AL Test Runner | AL0264/AL0197 fire (sometimes — racy on 45s window) |
| `cell-real-no-tr` | + AL LSP for Agents | AL0264/AL0197 fire (racy) |
| `cell-real-do-support` | All three | **AL0264 + AL0197 fire** |
| `cell-real-isolated-cache` | All three + alt cache dir | **AL0264 + AL0197 still fire** |

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

The false `AL0264` / `AL0197` "already declared by extension X" errors
**reproduce deterministically against a real BC project** (Continia's
`DO.Support-UISimply/Core` workspace) but **do not reproduce on any of
the four synthetic fixtures** we built (`fbemc-shape`, `pkg-collision`,
`multi-app-workspace`, `with-test-app`).

Critical observation: the bug fires in `cell-real-control` — that is,
**with only the Microsoft AL extension installed**, no AL LSP for Agents
wrapper, no AL Test Runner. This contradicts the bisection conclusion
in issue #17 ("disable AL LSP for Agents → bug goes away"); the wrapper
is **not** the cause. The bug is intrinsic to the project structure +
Microsoft AL LS interaction.

Likely root cause for the DO.Support-UISimply repro: the workspace
contains `Cloud/.dependencies/CC/` directories holding checked-in source
files of upstream extensions (Continia internal libraries copied into the
project for IDE convenience). The MS AL LS scans those `.al` files as if
they were source for the open project (`Continia Core`), causing object
declarations from those files to appear duplicated against the actual
project source.

Implication for Layer 1: `Option A` (`AL_LSP_ALT_EXT_DIR` isolation) does
not fix the bug — `cell-real-isolated-cache` shows it still fires.
`Option B` (drop wrapper's inner LS) cannot fix it either, since the bug
fires when the wrapper isn't even installed.

The right next step is upstream: either get DigiTecKid to inspect their
project for the same `.dependencies/` pattern, or have the wrapper offer
an opt-in middleware filter that drops `AL0264`/`AL0197` from forwarded
diagnostics for users who confirm their project hits this MS AL LS quirk.

The capability-parity contract test (`suite/parity.test.ts`) is present
but currently self-skips — `vscode.executeDefinitionProvider` returns
empty for AL files because the MS AL extension uses a custom
`al/gotodefinition` request, not standard LSP. Layer 1 must invoke
`al/gotodefinition` directly via the AL extension's LSP client.

## Known limitations

- AL extension cold-start can take 30+ seconds; the readiness state
  machine waits at least 45 s and at most 180 s after marking activity.
  Diagnostics that arrive after the wait window are missed (see racy
  fluctuation noted in `cell-real-no-wrapper` / `cell-real-no-tr`).
- Cells that need an external fixture path (`cell-real-*`) only run
  when that path exists locally. CI must mirror the path or skip.
- Microsoft AL extension licensing: the `.vsix` is downloaded from the
  public Marketplace at test time and cached locally. The cache is
  gitignored; nothing is redistributed.

## Harness fixes shipped with this work

- `--install-extension` in test-electron `launchArgs` is silently a
  no-op when `extensionTestsPath` is also set. Extensions must be
  pre-installed via a separate `code --install-extension` invocation.
  Without this fix, no marketplace extension was ever loaded for any
  cell — all baselines were empty for the wrong reason.
- VS Code 1.99 is too old for `ms-dynamics-smb.al@^1.100`; bumped lock
  to 1.103.0.
- The MS AL extension uses the `mcpConfigurationProvider` proposed API;
  pass `--enable-proposed-api ms-dynamics-smb.al` in launchArgs.
- AL Test Runner activation calls `getWorkspaceFolder()` which throws
  in multi-root workspaces unless an editor is active. Pre-open each
  workspace folder's `app.json` and `showTextDocument` it before
  opening any AL file.
- Diagnostic capture now reads from every URI VS Code knows about, not
  just opened ones — `AL0264` / `AL0197` can fire on URIs the test never
  explicitly opened.
