# Phase 1.5 — Decompilation Analysis

Decompiled the Microsoft AL extension's compiler/LS DLLs to identify the
exact code path that emits AL0264 / AL0197, and to locate the cause of
the false positives observed against the minimized reproducer (Phase
1.3).

Tooling: `ilspycmd 10.0.0.8330`. DLLs from
`%USERPROFILE%\.vscode\extensions\ms-dynamics-smb.al-18.0.2293710\bin\win32\`.

## Diagnostic emission site

`Microsoft.Dynamics.Nav.CodeAnalysis.CompilerDiagnosticsResources.resx`:

```
ERR_AppObjDuplicateName  →  "An application object of type '{0}' with
                              name '{1}' is already declared by the
                              extension '{2}'"   (= AL0197)

ERR_AppObjDuplicateId    →  "An application object of type '{0}' with
                              ID '{1}' is already declared by the
                              extension '{2}'"   (= AL0264)
```

Both are emitted from `Microsoft.Dynamics.Nav.CodeAnalysis.Symbols.SourceModuleSymbol`:

`ReportDuplicateNameErrorForSymbol` at SourceModuleSymbol.cs:743

```csharp
private static void ReportDuplicateNameErrorForSymbol(
    ObjectTypeSymbol symbol,
    IModuleSymbol moduleContainingDuplicate,
    DiagnosticBag diagnostics)
{
    // ...
    diagnostics.Add(
        ErrorCode.ERR_AppObjDuplicateName,
        symbol.GetLocation(),
        symbol.Kind,
        symbol.Name,
        moduleContainingDuplicate?.ToString()
            ?? CodeAnalysisResources.UnknownModulePlaceholder);
}
```

`ReportDuplicateId` at SourceModuleSymbol.cs:1075

```csharp
private static void ReportDuplicateId(
    ISymbolWithId symbol,
    IModuleSymbol moduleContainingDuplicate,
    DiagnosticBag diagnostics)
{
    Location location = (((ApplicationObjectTypeSymbol)symbol)
        .DeclaringSyntaxNode as ApplicationObjectSyntax)?.ObjectId?.GetLocation();
    diagnostics.Add(
        ErrorCode.ERR_AppObjDuplicateId,
        location ?? symbol.GetLocation(),
        symbol.Kind,
        symbol.Id,
        moduleContainingDuplicate);
}
```

## Algorithm: CheckForDuplicateIds

`SourceModuleSymbol.cs:768`:

```csharp
private void CheckForDuplicateIds(
    ImmutableArray<ISymbolWithId> symbols,
    DiagnosticBag diagnostics)
{
    var instance  = PooledDictionary<int, ISymbolWithId>.GetInstance();
    var instance2 = PooledDictionary<int, IModuleSymbol>.GetInstance();
    foreach (var current in symbols)               // input list
    {
        if (IsDuplicateIdAllowed(current, instance)) continue;
        if (instance.ContainsKey(current.Id))
        {
            if (!instance2.ContainsKey(current.Id))
                instance2.Add(current.Id, this);   // ←  "this" = current module
            ReportDuplicateId(current, this, diagnostics);   // ← FIRES HERE
        }
        else
        {
            instance.Add(current.Id, current);
        }
        // Walk reference modules to detect cross-extension duplicates...
    }
    // ...
}
```

Key observation: the `ReportDuplicateId(current, this, diagnostics)`
call passes `this` as the "module containing the duplicate". `this` is
the SourceModuleSymbol of the project being compiled. So the message
"already declared by extension Continia Core" reflects the LS's view
that the SAME extension declares the codeunit twice.

## Where `symbols` comes from

`CheckUniquenessWithinType` at line 626:

```csharp
private void CheckUniquenessWithinType(
    SymbolKind symbolKind,
    ImmutableArray<ContainerSymbol> symbols,
    DiagnosticBag diagnostics)
{
    CheckForDuplicateNames(symbolKind, symbols, diagnostics);
    if (!SemanticFacts.IsApplicationObjectWithoutId(symbolKind))
        CheckForDuplicateIds(
            symbols.Cast<ISymbolWithId>().ToImmutableArray(),
            diagnostics);
}
```

The caller (line 571):

```csharp
CheckUniquenessWithinType(
    current,
    base.SymbolMap.GetSymbolsByKind(current),
    diagnostics);
```

So `symbols` = `SymbolMap.GetSymbolsByKind(SymbolKind.Codeunit)` — the
list of all codeunit `ContainerSymbol`s in this module's `SymbolMap`.

**For the bug to fire, the SymbolMap has to contain TWO ContainerSymbols
with the same Codeunit Id (6225384 in our repro).**

## How SymbolMap is built

`SourceNamespaceSymbol.MakeSymbolMap` at line 424:

```csharp
private SymbolMap<ContainerSymbol> MakeSymbolMap(DiagnosticBag diagnostics)
{
    var builder = SymbolMap<ContainerSymbol>.CreateBuilder();
    foreach (var current in mergedDeclaration.Children)
    {
        BuildSymbolsFromDeclaration(current, builder, diagnostics);
    }
    return builder.ToMap();
}
```

`BuildObjectSymbolsFromDeclaration` at line 454:

```csharp
private void BuildObjectSymbolsFromDeclaration(
    MergedObjectDeclaration declaration,
    SymbolMap<ContainerSymbol>.Builder builder,
    DiagnosticBag diagnostics)
{
    foreach (SyntaxReference syntaxRef in declaration.SyntaxReferences)
    {
        SyntaxNode syntax = syntaxRef.GetSyntax();
        switch (declaration.Kind)
        {
            case DeclarationKind.Codeunit:
                builder.AddSymbol(
                    new SourceCodeunitTypeSymbol(this, (CodeunitSyntax)syntax));
                break;
            // ...other kinds...
        }
    }
}
```

**This is the bug site.** For each `MergedObjectDeclaration`, the method
iterates `declaration.SyntaxReferences` and adds a NEW symbol per
reference. If a single logical object (e.g. Codeunit 6225384) has
multiple `SingleObjectDeclaration` entries inside its
`MergedObjectDeclaration` — even though they share the same identity
(kind + id + name) — each one becomes a separate `ContainerSymbol` in
the SymbolMap. `CheckForDuplicateIds` then correctly observes them as
duplicates and fires AL0264.

By comparison, MergedNamespaceDeclaration.MakeChildren (line 82)
explicitly dedups by identity into a Dictionary. The merging *does*
happen at the declaration level. The bug is that
`BuildObjectSymbolsFromDeclaration` then "un-merges" by iterating
SyntaxReferences instead of taking one symbol per merged declaration.

`MergedObjectDeclaration.SyntaxReferences` (MergedObjectDeclaration.cs:18):

```csharp
public ImmutableArray<SyntaxReference> SyntaxReferences =>
    Declarations.SelectAsArray((SingleObjectDeclaration r) => r.SyntaxReference);
```

So the per-reference iteration in `BuildObjectSymbolsFromDeclaration`
yields one symbol for every SingleObjectDeclaration the merger collected.
If two SingleObjectDeclarations made it through the merger (same
identity, two different syntax trees), the SymbolMap now contains two
ContainerSymbols.

## Why two SingleObjectDeclarations exist for one logical object

Each SyntaxTree gets one DeclarationTreeBuilder run
(DeclarationTreeBuilder.cs:18):

```csharp
public static RootSingleNamespaceDeclaration ForTree(SyntaxTree syntaxTree)
{
    return (RootSingleNamespaceDeclaration)
        new DeclarationTreeBuilder(syntaxTree).Visit(syntaxTree.GetRoot());
}
```

One SyntaxTree per file → one SingleObjectDeclaration per object in
that file. Two SingleObjectDeclarations for the same object means **two
SyntaxTrees containing the same source object**.

`Compilation.AddSyntaxTrees` (Compilation.cs:266) rejects re-adding the
exact same SyntaxTree instance:

```csharp
PooledHashSet<SyntaxTree> instance = PooledHashSet<SyntaxTree>.GetInstance();
instance.AddAll(syntaxAndDeclarationManager.ExternalSyntaxTrees);
foreach (SyntaxTree item in list)
{
    if (instance.Contains(item))
    {
        throw new ArgumentException(
            CodeAnalysisResources.SyntaxTreeAlreadyPresent, ...);
    }
    instance.Add(item);
}
```

But it does NOT reject adding a different SyntaxTree instance whose
underlying source happens to be the same file path. So:

- if AL LS parses a file via filesystem scan, it creates SyntaxTree A
- if AL LS later receives `textDocument/didOpen` for the same path and
  parses the buffer content, it can create SyntaxTree B
- both SyntaxTree A and SyntaxTree B end up in the Compilation
- both produce a SingleObjectDeclaration for Codeunit 6225384
- MergedObjectDeclaration merges them by identity into one node, but
  keeps both SyntaxReferences
- BuildObjectSymbolsFromDeclaration emits two ContainerSymbols
- AL0264 / AL0197 fires

## Workspace state shape

`Microsoft.Dynamics.Nav.CodeAnalysis.Workspaces.ProjectState.AddDocument`
(ProjectState.cs:579) keys documents by `DocumentId`:

```csharp
public ProjectState AddDocument(DocumentState document)
{
    var projectInfo = ProjectInfo
        .WithVersion(Version.GetNewerVersion())
        .WithDocuments(ProjectInfo.Documents.Concat(document.Info));
    ImmutableArray<DocumentId> documentIds =
        DocumentIds.ToImmutableArray().Add(document.Id);
    ImmutableDictionary<DocumentId, DocumentState> documentStates =
        DocumentStates.Add(document.Id, document);
    return With(projectInfo, documentIds, default, documentStates);
}
```

There is no FilePath uniqueness check here. Two `DocumentState` objects
with different `DocumentId`s but the same FilePath can co-exist.
`UpdateDocument` (line 619) only replaces by Id — adding a different-Id
document for the same path leaves the original in place.

This matches the bug surface: when the LS receives `didOpen` for a
file it has already discovered via FS scan, the LS can end up with two
DocumentStates pointing at the same path.

## Fix candidates (for the Microsoft side)

Three places where the bug could be guarded:

1. **`BuildObjectSymbolsFromDeclaration` should produce one symbol per
   `MergedObjectDeclaration`, not one per SyntaxReference.** This is the
   most-localized fix. Existing duplicate detection in `CheckForDuplicateIds`
   would then fire only for genuine multi-source-file declarations of
   the same id (which AL doesn't allow, so the existing diagnostic
   makes sense). Risk: code paths that rely on `SyntaxReferences` being
   plural here may need updating.

2. **`Compilation.AddSyntaxTrees` should also reject syntax trees whose
   path matches an existing tree.** This is the most defensive fix. Risk:
   may break legitimate scenarios (e.g. two files with the same name in
   different folders that happen to map to the same display path).

3. **`ProjectState.AddDocument` should refuse documents whose FilePath
   collides with an existing document.** This is the most upstream fix.
   Risk: same as (2). Also requires the LS to call `UpdateDocument`
   instead of `AddDocument` when it discovers the path is already known.

## Workspace-state observations needed to confirm

The decompilation tells us the sufficient condition (two SyntaxTrees
for the same file in the Compilation), but we have not directly
observed the LS state to confirm this is what happens in our repro.
Two ways to confirm:

- **LSP wire trace**: capture all JSON-RPC frames between VS Code and
  AL LS during a reproducing run. Look for `textDocument/didOpen` for
  `AppExperience.Codeunit.al` AND for any prior frame that delivers
  the same file content (e.g. `workspace/didChangeWatchedFiles`,
  custom protocol messages). The AL LS does not implement standard
  `workspaceFolders` rescans the same way as other LSPs, so the
  trace may show an unfamiliar handshake.

- **Custom debug build of the wrapper**: have the wrapper log the
  observed traffic (it sits in the middle for some requests, though
  not all). This is the path described in Phase 1 of the plan but
  has not been built yet.

## Open questions

1. Is the duplicate SyntaxTree creation deterministic given a workspace
   layout, or does it depend on event timing? (Phase 1.3 found the bug
   IS deterministic given enough workspace files, racy with few — this
   suggests timing matters.)
2. Why do empty-stub bodies not trigger? If only file count and didOpen
   matter, content shouldn't. Hypothesis: with empty bodies, the LS
   completes initial parsing fast enough that it doesn't accept the
   didOpen content as a "new" tree (it deduplicates via some content
   hash or version check we haven't found). Larger bodies = longer
   parsing = the second tree doesn't match the first by whatever
   identity check exists, so it's added.
3. Are the 11 specific real Continia files special, or is the trigger
   simply "enough total parse time"? Unverified.

## Status

Phase 1.5 is far enough to file the upstream bug. The MS report can
include:
- The minimal reproducer (Phase 1.3, with Continia code)
- This decompilation analysis (no Continia code needed; cites MS
  internal class names)
- Three fix candidates with trade-offs
- Open questions for MS to address with their internal knowledge

For an in-tree synthetic minimal fixture (Phase 1.6), we would need to
either:
- Get permission from Continia to ship the 11 source files, or
- Construct a synthetic fixture that triggers the same SyntaxTree
  duplication. This requires understanding *what* makes the LS
  produce the second SyntaxTree, which requires the wire trace from
  Open Question 1. Not blocked from filing the upstream bug.
