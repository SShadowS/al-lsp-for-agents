# Spike: resolving other apps from source (project references)

Date: 2026-09-23. AL extension 18.0.2732683 (host v18.0.41.45789), Windows.
Probe: `test-al-project/probe_source_refs.py` (drives the AL LS directly, no wrapper).
Test tree: `U:\Git\DO.Support-alcall` (no git, no `.alpackages` anywhere, same as
DevOpsWorker review containers). Active project: `DocumentCapture/Cloud`.

## Question

DevOpsWorker review containers have sibling repos as source but no symbol
packages. The AL LS only resolves other apps through `.alpackages`, so hover,
definition and references on anything from another app return nothing. Can the
AL LS resolve those apps from their source instead, the way VS Code does for a
multi-root workspace ("project references")?

## Answer

Yes. It works well for Continia-to-Continia references. With some extra work it
also works for the Base App from source, but that's expensive.

## How VS Code does it (from `dist/extension.js`)

1. `getProjectReferences(folder)`: for each `app.json` dependency, find another
   workspace folder whose manifest has the same app id and a version >= the
   dependency version (`hasMatch`).
2. `getClosure(active)`: transitive walk over those references. Sent as
   `activeWorkspaceClosure`. The references go in `expectedProjectReferenceDefinitions`.
3. `al/setActiveWorkspace` with those settings.
4. The server sends the request `al/activeProjectLoaded`. The client answers by
   running `loadProjectReferences`. For every (reference, parent) pair it sends
   `workspace/didChangeConfiguration` with `setActiveWorkspace:false` and
   `dependencyParentWorkspacePath:<parent>`. It dedups per pair, not per folder.
   A shared dependency like Core gets one config per project that references it.

Only `dependencies` count. The `application` property is never turned into a
project reference.

## Results

Closure used: DC, Core, DeliveryNetwork, DocumentOutput, DN Onboarding (1744 .al
files). Nine dependencies still missing from source (Continia System
Application, the Continia AI apps, Business Foundation, ...).

| Probe (in DC source) | Baseline (today) | Source refs |
|---|---|---|
| hover/definition `"CSC Basic"` permission set (Core) | null / [] | resolves, jumps to `Core/Cloud/PermissionSets/Basic.PermissionSet.al` |
| hover/definition `"CTS-CDN Basic"` (DeliveryNetwork) | null / [] | resolves |
| hover/definition `Codeunit "CSC XML Document"` | null / [] | resolves |
| definition of `CSCXmlDoc.FromSysXmlDocument` | [] | `XMLDocument.Codeunit.al:408` |
| references of `FromSysXmlDocument` | 0 | 66 locations across Core, DN, DC |
| `"CTS-CBF Read"` (app not in source) | null | null (correct, app absent) |
| `Vendor` / `Vend.Get` (Base App) | null | null without BC source (see below) |
| errors in `CDCBasic.PermissionSet.al` | 42 | 40 (the two resolvable ones gone) |
| load, closure-loaded | 0.5 s | 0.7 s |
| peak memory | 490 MB | 730 MB |
| first cross-app hover | n/a | ~2 s cold, then ms |
| references on a Core procedure | n/a | 3.2 s cold |

A missing app degrades per symbol, not per project. Types from absent apps show
as blank in hover (`var SysXmlDoc: )`). Everything else keeps working.

### Base App from source

The BC source has `$(app_currentVersion)` / `$(app_platformVersion)`
placeholders in `app.json`. The spike used a patched copy (versions set to
28.5.54151.0). DC reaches the Base App only through `"application": "28.0.0.0"`,
so I mapped `application` to a dependency on the `Application` app
(`c1335042-...`). That app has no source and depends on Base App, System
Application and Business Foundation. VS Code doesn't do this mapping, but the
server accepts it.

| | Continia refs only | + BC source via `application` |
|---|---|---|
| `Vendor` hover | null | full field list; definition goes to Base App source |
| errors, `CDCAdvancedAppvlManagement.Codeunit.al` | 114 | 10 |
| errors, `CDCBasic.PermissionSet.al` | 40 | 7 |
| files with diagnostics published | 2682 | 12198 |
| peak memory | 730 MB | 2.6 GB |
| first hover after load | 1.9 s | 17 s |
| references, cold | 3.2 s | 15 s |

The remaining errors are platform tables (`AllObj`, `Field`, `AllObjWithCaption`)
and Continia apps not in the tree. Platform tables only exist in `System.app`
symbols. No source tree can ever provide them.

## Traps found

1. **Per-parent reference configs are mandatory once a dependency has its own
   references.** If `didChangeConfiguration` for a reference is deduped per
   folder (or skipped), navigation still works, but the server silently never
   publishes diagnostics. It stays idle at 0% CPU with no error. Flat closures
   (DC+Core) work either way. Nested ones (DC→DN→Core) need Core configured
   once with parent DC and once with parent DN.
2. **Plain multi-root is not enough.** Registering the dependency folders as
   workspace folders without a closure gives navigation, but references miss
   the other dependencies (63 locations instead of 66; DeliveryNetwork usages missing).
3. **Version placeholders.** `$(...)` versions fail `hasMatch`, so those projects
   are skipped. The BC source repo needs substitution, or has to stay out of scope.
4. **Duplicate app ids.** Cloud/OnPrem flavours, and two DN tool apps, share
   ids. Picking a candidate needs a rule (the spike prefers a path containing "Cloud").
5. **Symbol search is broken independently.** `al/symbolSearch` hangs with no
   response in every mode, including baseline. That's the known NRE with missing
   transitive deps (see `project_al_symbolsearch_nre`). `workspace/symbol`
   also hangs once Core is loaded. The wrapper's workspaceSymbol goes through
   `al/symbolSearch`, so it's already dead in these containers today. A symbol
   cache fixes the baseline hang, but with source refs on it still hangs (see
   the follow-up below).
6. **`hasProjectClosureLoadedRequest` says loaded after under 1 s**, but the first
   real query pays the compile (2 s Continia only, 17 s with Base App). The
   readiness signal is not a readiness signal.

## Project switching

Activating Core and then DC again takes ~0.1 s each way. Loaded projects stay
resident. With Core active, references on a Core procedure still return the DC
usages from the earlier load. The wrapper's existing active-project flip on
file access is therefore not a problem.

Reverse direction not tested: a cold start with Core active can't see DC,
because DC is not in Core's closure. VS Code behaves the same. For a PR against
Core, "who uses this" across dependents would need extra folders loaded beyond
the closure.

## Implications for the wrapper (if built)

- Opt-in only (`--source-roots` / `AL_LSP_SOURCE_ROOTS`). Unset means today's
  payload byte-for-byte. Guard it with a unit test.
- Scan roots for `app.json` (skip `.alpackages`), index app id to folder, and
  build the closure with the VS Code `hasMatch` rule.
- Add closure folders to `workspaceFolders` in `initialize`, and set
  `activeWorkspaceClosure` + `expectedProjectReferenceDefinitions`.
- Handle `al/activeProjectLoaded` (today it gets the default null ack), and send
  per-(reference, parent) configs. Without this, diagnostics break silently (trap 1).
- The `application` mapping and placeholder substitution are optional extras,
  worth it only when BC source is present and 2.6 GB is acceptable. The cheaper
  fix for base-app symbols in DevOpsWorker is still `.alpackages` from the MS
  symbol feed.
- Not tested: Linux/`dotnet` launch (same host code, expected identical),
  Continia AI companion repo layout, very large closures.

## Reproduce

```
cd test-al-project
python probe_source_refs.py --mode baseline
python probe_source_refs.py --mode refs
python probe_source_refs.py --mode refs --exclude DocumentOutput   # nested closure
python probe_source_refs.py --mode multiroot
python probe_source_refs.py --mode refs --switch-to "Core\Cloud"
python probe_source_refs.py --mode refs --map-application --roots Core DeliveryNetwork DocumentOutput DocumentCapture <patched-bc-dir>
```

## Follow-up: symbol cache (same day)

DevOpsWorker checked the feed from a review container: MSSymbols is anonymous and
reachable. Package ids (w1, lower-case on flat2):

- `microsoft.platform.symbols` (System.app, 28.0.x only)
- `microsoft.application.symbols`
- `microsoft.baseapplication.symbols.437dbf0e-84ff-417a-965d-ed2bb9650972`
- `microsoft.systemapplication.symbols.63ca2fa4-4f03-4f2b-a480-172fef340d3f`
- `microsoft.businessfoundation.symbols.f3552374-a1f2-4356-848e-196002525837`

Each nupkg is a zip with one `.app` at the root. ~6.9 MB per BC minor. No 29.x
yet. alc/altool have no symbol download command.

The wrapper now reads `AL_LSP_PACKAGE_CACHE`, which appends extra
`packageCachePaths` entries. Local e2e used a BC 28.0 `.app` set as the external cache:

| | no cache | cache | cache + source refs |
|---|---|---|---|
| `Vendor` hover | null | resolves | resolves |
| errors `CDCAdvancedAppvlManagement` | 114 | 5 | 5 |
| errors `CDCCaptureManagement` | 89 | 68 | 35 |
| errors `CDCBasic.PermissionSet` | 42 | 7 | 5 |
| `al/symbolSearch` | hangs | answers (17 s cold) | hangs |
| peak memory | 490 MB | 1.75 GB | 2.0 GB |

The cache fixes the baseline symbolSearch hang. With source refs, the hang comes
back. The likely cause is Core source lacking Continia System Application, which
ContiniaBase provides in the real layout. That needs a retest there before phase 3.

## Phase 3 findings (built as `AL_LSP_SOURCE_ROOTS`)

- **Precedence:** with Continia Core as both a source project and a `.app` in the
  package cache, definition goes to the source. Stale packages don't shadow a
  PR's source.
- **Workspace folders are not needed.** Only the active project as a workspace
  folder, plus the closure settings, gives the same resolution, references and
  diagnostics. `initialize` stays as it is.
- **The "symbol search hang" was two things, neither a real hang.**
  1. `workspace/symbol` never answers once Core is loaded (Core alone is
     enough; DeliveryNetwork alone answers). My probe sent it first, and the
     `al/symbolSearch` queued behind it looked hung too. The wrapper never
     sends `workspace/symbol` (it maps workspaceSymbol to `al/symbolSearch`),
     so this doesn't affect it.
  2. The first `al/symbolSearch` of a session builds the symbol index over the
     whole closure plus packages: 24 s with Base App symbols, 43-45 s with the
     four Continia source projects on top, then milliseconds. The wrapper's
     default 30 s request timeout cut it off. Fixed with a 120 s timeout for
     `al/symbolSearch`. Building the index costs memory: the AL LS went from
     938 MB to 2,038 MB, paid only when workspaceSymbol is used.
- Unrelated bug found on the way: on Windows al-sem publishes diagnostics under a
  URI with lower-cased directories, and `DiagnosticMerger` keys by the exact
  string, so AL LS and al-sem diagnostics for one file don't merge.
