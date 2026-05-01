# Issue #17 Investigation Log

Chronological log of every experiment and finding for the false
`AL0264` / `AL0197` "already declared by extension X" diagnostics
reported in https://github.com/SShadowS/al-lsp-for-agents/issues/17.

Dates are absolute. Each entry: timestamp, what was tried, result.

## 2026-05-01

### Setup discovery

- Existing matrix harness has 5 cells (cell-control, cell-with-wrapper,
  cell-with-test-runner, cell-all-three, cell-isolated-cache). All
  baselines committed as empty `[]` diagnostics.
- Existing `test-al-project/` fixture is single-folder, single-app,
  ~10 AL files. Plan called for expanding to multi-app fixtures
  resembling DigiTecKid's "FBEMC - Main" project.

### Built four synthetic multi-app fixtures

Each tests a different hypothesis for what triggers AL0264:

| Fixture | Hypothesis tested | Result |
|---|---|---|
| `fbemc-shape` | Source + dependent app via .app symbol triggers double-registration | 0 diagnostics — bug NOT reproduced |
| `pkg-collision` | Two .app files with same publisher/name/version, different ids | 0 diagnostics — MS LS dedupes by app id |
| `multi-app-workspace` | Two unrelated AL apps in multi-root | 0 diagnostics |
| `with-test-app` | Main + Test app depending on main, mimicking AL Test Runner output | 0 diagnostics |

None of the synthetic fixtures reproduced AL0264 / AL0197.

### Discovered harness was broken

While debugging the empty results, found that test-electron's
`launchArgs: ["--install-extension", path]` is silently a no-op when
`extensionTestsPath` is also set. Verified by inspecting
`harness/out/extensions/<cell>/extensions.json`: empty array. **No
marketplace extension was ever installed in any cell** before this
work. The 5 pre-existing baselines were `[]` for the wrong reason —
the AL extension never loaded.

Fix: pre-install via separate `code --install-extension` invocation
using `downloadAndUnzipVSCode` + `resolveCliArgsFromVSCodeExecutablePath`
from `@vscode/test-electron`, before calling `runTests`.

Other harness bugs found and fixed in the same pass:

- VS Code 1.99 too old for `ms-dynamics-smb.al@^1.100`. Bumped lock to
  1.103.0.
- AL extension uses `mcpConfigurationProvider` proposed API. Need
  `--enable-proposed-api ms-dynamics-smb.al` in launchArgs.
- AL Test Runner activation calls `getWorkspaceFolder()` which throws
  in multi-root workspaces if there's no active editor when the AL
  language registration triggers TR's onLanguage:al activation. Fix:
  pre-open and `showTextDocument` each workspace folder's `app.json`
  before opening any AL file.
- Diagnostic capture restricted to opened URIs. Switched to capturing
  from every URI VS Code knows about — AL0264/AL0197 can fire on URIs
  the test never explicitly opened.

After fixes, all 5 original cells re-recorded with real diagnostics
(35 entries on the default fixture, mostly AL1022 missing-package
and AL0185).

### Reproduced against real BC project

User offered access to `u:\Git\DO.Support-UISimply\` — real
multi-app Continia BC project with `Core/CoreBase.code-workspace`
listing `Cloud` (Continia Core source) + `Test` (Continia Core Test
Suite, depends on Cloud via .app symbol).

Added external-fixture support to harness (`CellConfig.fixture` can
be an absolute path or relative-to-repo-root). Added cell pointing at
this real project: `cell-real-do-support`.

**First run reproduced AL0264 + AL0197 in the recorded baseline:**

```json
{ "relUri": "Cloud/AL/Activation/AppExperience.Codeunit.al",
  "source": "AL", "code": "AL0264", "severity": "Error",
  "line": 0, "character": 9 },
{ "relUri": "Cloud/AL/Activation/AppExperience.Codeunit.al",
  "source": "AL", "code": "AL0197", "severity": "Error",
  "line": 0, "character": 17 }
```

Errors fire on line 0 of `AppExperience.Codeunit.al` — the codeunit
declaration line.

### Determined wrapper not directly causal

Built four control cells against the same real project to bisect
which extension combination is required:

| Cell | Extensions | AL0264 fires |
|---|---|---|
| `cell-real-control` | MS AL only | yes (deterministic) |
| `cell-real-no-wrapper` | MS AL + AL Test Runner | yes (racy on first run, fired on retry) |
| `cell-real-no-tr` | MS AL + AL LSP for Agents | not on first run (single sample) |
| `cell-real-do-support` | all three | yes (deterministic) |
| `cell-real-isolated-cache` | all three + `AL_LSP_ALT_EXT_DIR` | yes |

**Critical finding:** the bug fires with **only the Microsoft AL
extension installed** — no wrapper, no Test Runner. Bisection in
issue #17 ("disable AL LSP for Agents → bug goes away") was likely
incidental — process startup timing, not causation. Option A
(`AL_LSP_ALT_EXT_DIR` extension cache isolation) does NOT fix it.
Option B (drop wrapper's inner LS) cannot fix it (the bug fires
without the wrapper installed at all).

### Identified candidate root cause

Inspected the DO.Support-UISimply/Core layout:

- `Cloud/.dependencies/CC/Codeunit/`, `Cloud/.dependencies/CC/Page/`,
  `Cloud/.dependencies/CC/Table/` — these directories contain
  checked-in **.al source files** copied from upstream extensions
  (Continia internal libraries) for IDE convenience.
- The MS AL Language Server appears to scan these `.al` files as if
  they were source for the open project (`Continia Core`), causing
  declarations from those upstream-source files to register against
  the open project's extension identity. When the same object is
  declared in both `Cloud/AL/...` (the real source) and somewhere
  reachable via this scan, the LS reports "already declared by
  extension Continia Core" — exactly the AL0264 / AL0197 message.

This is a working hypothesis, not yet verified by decompilation or
upstream confirmation. Decompilation of
`Microsoft.Dynamics.Nav.CodeAnalysis.dll` at the AL0264 emission
site is the next planned experiment.

### Decision: investigate to upstream-report quality

User holds Microsoft MVP status (BC/AL, granted on 2026-05-01) and
has direct upstream reporting access. Decision made not to ship any
silent suppression / middleware filter for AL0264 / AL0197. Instead:
reproduce, minimize, decompile, document, file via MVP channel.

This document is the start of that effort.

### Status at end of 2026-05-01

- Harness machinery known-working, with regression cells in place.
- One real reproducer cell (`cell-real-control`) confirmed to fire
  the bug deterministically against the full DO.Support-UISimply/Core
  workspace (132 MB, external to repo).
- No minimized in-tree fixture yet — Phase 1.3 next.
- Wrapper involvement still uncertain (correlation observed in user
  reports, no direct mechanism proven).

Next: Phase 1.3 — bisect DO.Support-UISimply/Core down to the
smallest committable fixture that still triggers AL0264 with only
the Microsoft AL extension installed.
