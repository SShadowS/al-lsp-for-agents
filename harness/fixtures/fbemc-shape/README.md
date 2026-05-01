# fbemc-shape fixture

Reproduces the project shape from issue #17 (DigiTecKid).

- `main-app/` — the "FBEMC - Main" extension. Codeunit 75000 named "General Event Subscriptions".
- `dependent-app/` — depends on FBEMC - Main via `.alpackages/Dynamic Technology Partners, Inc._FBEMC - Main_1.0.0.0.app`.
- `fbemc.code-workspace` — opens both folders multi-root.

The compiled `.app` files are committed (small, deterministic, avoid requiring `alc.exe` in CI). To rebuild:

```bash
ALC=/c/Users/SShadowS/.vscode/extensions/ms-dynamics-smb.al-18.0.2293710/bin/win32/alc.exe
MSYS_NO_PATHCONV=1 "$ALC" /project:./main-app /packagecachepath:./main-app/.alpackages /generatecode+ /out:./main-app/fbemc-main.app
cp main-app/fbemc-main.app "dependent-app/.alpackages/Dynamic Technology Partners, Inc._FBEMC - Main_1.0.0.0.app"
MSYS_NO_PATHCONV=1 "$ALC" /project:./dependent-app /packagecachepath:./dependent-app/.alpackages /generatecode+ /out:./dependent-app/fbemc-dependent.app
```

Bug hypothesis: when the workspace contains `FBEMC - Main` as a SOURCE project AND its compiled `.app` is also visible (here via the dependent-app's `.alpackages`), the MS AL LS may register the extension twice — triggering AL0264/AL0197.
