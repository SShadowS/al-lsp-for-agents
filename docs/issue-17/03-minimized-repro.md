# Phase 1.3 — Minimized Reproducer

Goal of this phase: shrink the real `DO.Support-UISimply/Core` workspace
(132 MB, ~thousands of files) down to the smallest set of inputs that
still triggers AL0264 / AL0197 against MS AL extension only (no AL LSP
for Agents wrapper, no AL Test Runner).

## Result

**The minimal reliably-reproducing workspace is 11 real Continia AL
files + their app.json + Cloud/.alpackages with Microsoft + Continia
System Application symbol packages.**

Without `.alpackages`: same 11 files reproduce ~80% of runs (4/5 in
sampled run). With `.alpackages`: 5/5 deterministic.

The 11 files (all under `Cloud/AL/Activation/` in the original repo):

```
ActivationWizardCloud.Page.al        page 6192811
AppExperience.Codeunit.al            codeunit 6225384  ← OPENED, fires here
AppExperienceImpl.Codeunit.al        codeunit 6225385
AppExperienceType.Enum.al            enum 6192774
ClientCredentialsCloud.Page.al       page 6192836
CloudCompanyActivations.Page.al      page 6192806
CloudCredentials.Page.al             page 6192802
CloudInstallManagement.Codeunit.al   codeunit 6225398
CloudSubscriptionMgt.Codeunit.al     codeunit 6225386
CloudSubscriptionMgt.Page.al         page 6192803
CompActWizardCloud.Page.al           page 6192816
```

The bug fires at line 0 of the OPENED file
(`AppExperience.Codeunit.al`) with messages:

```
AL0197  An application object of type 'Codeunit' with name
        'CSC App Experience' is already declared by the extension
        'Continia Core by Continia Software (28.0.0.0)'

AL0264  An application object of type 'Codeunit' with ID '6225384'
        is already declared by the extension 'Continia Core by
        Continia Software (28.0.0.0)'
```

## Inputs verified NOT required

- The `Test/` folder (sibling project depending on Cloud via .app)
- Multi-root `CoreBase.code-workspace` — single-root on Cloud reproduces
- `Cloud/.dependencies/CC/` (checked-in upstream-extension source)
- `Cloud/Continia Software_Continia Core_28.0.0.0.app` at workspace root
- Parent `Core/.alpackages/` (containing the project's own .app)
- The other 11 sibling subdirs under `Cloud/` (`Authentication/`, `DB/`,
  `EnumExtensions/`, `Images/`, `Install/`, `Libraries/`,
  `PermissionSets/`, `Source/`, `Specialization/`, `Translations/`,
  `Upgrade/`, `UserControls/`)
- The other 12 sibling subdirs under `Cloud/AL/` (`Auth/`, `Cloud/`,
  `Cloud Migration/`, `Environment/`, `Environment Cleanup/`,
  `ErrorInfo/`, `Hub/`, `License/`, `OnPrem/`, `Usage/`, `Video/`)
- The other 16 files under `Cloud/AL/Activation/` (`CompanySetupCard`,
  `CoreCountryManagement`, `EndSubscrWizardCloud`, `InternalApp*`,
  `InvoicingSetupPart`, `Manage*`, `Migrate*`, `Module*`, `Solution*`,
  `TempClient*`, `Trial*`, `Wizard*`)
- AL LSP for Agents wrapper extension
- AL Test Runner extension

## Inputs that DO matter

- **The 11 real Continia .al files** with their actual code bodies.
  Replacing all 11 bodies with empty stubs (`codeunit X "Y" { }`) drops
  the repro to 0/3 runs. Specific code paths inside the bodies are
  required.
- **App opened via `textDocument/didOpen`** matters. With the same 11
  files in the workspace but only opening `app.json` (no `.al` file
  via didOpen), AL0264/AL0197 do **not** fire. The bug requires the
  source file to be sent to the LS via didOpen, suggesting the LS
  registers the codeunit twice — once via filesystem scan, once via
  didOpen — without dedup.
- **File count enough to slow down the LS.** With only 3 files
  (AppExperience + AppExperienceImpl + CompActWizardCloud), bug doesn't
  fire (0/2 runs). With 11 specific files, it fires reliably.

## Bisection trail

| Step | Inputs kept | Inputs removed | AL0264/AL0197 | Notes |
|------|-------------|----------------|---------------|-------|
| start | full Core/ multi-root via CoreBase.code-workspace | nothing | fires | baseline (cell-real-control already established) |
| axis 1 | only Cloud (single-root) | Test/ | fires | Test irrelevant |
| axis 3 | only Cloud, no .dependencies | .dependencies/CC | fires | .dependencies irrelevant |
| | only Cloud, no .dependencies, no self.app | Continia Software_Continia Core .app at Cloud root | fires | self.app irrelevant |
| | only Cloud, no parent .alpackages | parent Core/.alpackages | fires | parent .alpackages irrelevant |
| axis 4 | Cloud bare (only AL/ subtree + app.json) | sibling dirs Authentication, DB, etc. | fires | sibling dirs irrelevant |
| | only Cloud/AL/Activation/ subtree | sibling AL/ subdirs (Auth, Cloud, etc.) | fires | other AL/ subdirs irrelevant |
| | 14 of 27 Activation files (alphabetical first half) | 13 last-half files | fires | half is enough |
| | 7 of 27 (smaller) | 20 files | does NOT fire | |
| | 10 of 27 (1-10 alphabetically) | 17 files | does NOT fire | needs file 11 |
| | 11 (1-10 + CompActWizardCloud) | 16 files | fires | CompActWizardCloud is one trigger |
| | 12 (1-10 + CompActWizardCloud + CompanySetupCard) | 15 files | fires | also fires with extra |
| | 11 (no AppExperienceImpl) | adds back rest | does NOT fire | AppExperienceImpl IS required |
| | 3 (AppExperience + AppExperienceImpl + CompActWizardCloud) | only those | does NOT fire | 3 files insufficient |
| **end** | **11 files + app.json + .alpackages** | everything else | **5/5 fires** | minimal reliable repro |
| (variant) | 11 files + app.json, NO .alpackages | nothing else | 4/5 fires | smaller, slightly racy |
| (verification) | 11 files with EMPTY BODIES | only stubs | 0/3 fires | content matters, not count |

## Why timing matters

When the workspace has very few files, the LS finishes its initial
filesystem scan quickly and the `textDocument/didOpen` handler arrives
when the symbol table is in a "post-scan, pre-didOpen" state without
the duplicate-detection trigger. With more files, the FS scan is still
in progress when `didOpen` arrives, and the LS appears to add the
opened file's symbols to the table without first removing the
FS-scanned entries — producing the duplicate.

This is consistent with a race between two AL LS pipeline stages:
1. background workspace scanner — registers source object declarations
   from FS-walked .al files
2. didOpen handler — registers source object declarations from the
   text content sent in the LSP `textDocument/didOpen` notification

If both register independently and the second doesn't replace the
first, the symbol table ends up with two entries for the same object
— exactly what AL0264/AL0197 detect.

## Open questions for Phase 1.5 (decompilation)

1. Where in `Microsoft.Dynamics.Nav.CodeAnalysis.dll` (or
   `Microsoft.Dynamics.Nav.EditorServices.LanguageServer.dll`) is the
   AL0264 emission? Confirm the message template hits and find the
   conditional that triggers it.
2. What datastructure holds "registered objects per extension"? Where
   is the registration call invoked from (FS scan path vs didOpen path)?
3. Is there a guard intended to dedup but failing? Or is dedup absent?
4. Why does file content matter (empty stubs don't trigger)? Is there
   a code path that only registers if certain syntax features are
   present in the source?

## Reproduction recipe (for MS bug report)

Pre-requisites:
- Windows 11, VS Code 1.103.x
- Microsoft AL extension 18.0.2190758 (or later)
- The 11 source files above (real Continia code; cannot be redistributed
  without permission from Continia Software)

Steps:
1. Create a directory `repro/Cloud/AL/Activation/` and place the 11 .al
   files there.
2. Place a valid `Cloud/app.json` declaring extension `Continia Core` by
   `Continia Software` version `28.0.0.0`, ID range covering 6192774 to
   6225398.
3. (Optional, for 100% reproducibility) Place `Microsoft_*.app` symbol
   packages and `Continia Software_Continia System Application_28.0.0.0.app`
   in `Cloud/.alpackages/`.
4. Open VS Code on the `Cloud/` folder (single-root).
5. Open `Cloud/AL/Activation/AppExperience.Codeunit.al` — the file
   immediately shows two errors at line 1 (the `codeunit 6225384`
   declaration line):
   - AL0197 "already declared by extension Continia Core"
   - AL0264 "already declared by extension Continia Core"

Expected: no errors. The file is the only declaration of codeunit
6225384 anywhere in the workspace; nothing else (source, symbol
package, or otherwise) declares it.

## State of the work directory

`harness/work/Core/Cloud/` is currently in the
"5/5 reproduces" minimal state:
- `app.json`
- `AL/Activation/` containing the 11 files above
- `.alpackages/` with MS + Continia System Application packages
- `.dependencies/` and `Continia Software_Continia Core_28.0.0.0.app`
  also present (restored after bisection — they don't affect the
  outcome but match the original layout)

Original full project untouched at `u:\Git\DO.Support-UISimply\Core/`.
