# `al/symbolSearch` silently hangs when transitive dependencies are missing

**Status:** Not yet filed upstream (draft for review)
**Confirmed in:** AL extension `ms-dynamics-smb.al-18.0.2293710` (Editor Services Host v18.0.35.20)
**Symptom:** No JSON-RPC response ever sent. Client waits indefinitely.
**Workaround in wrapper:** ancestor `.alpackages` auto-discovery (`DiscoverPackageCachePaths` in `wrapper/project.go`).

## Reproduction

1. Create or open an AL project whose `app.json` declares a dependency on an extension app (e.g. Continia Core).
2. Place ONLY that direct dependency's `.app` file in `.alpackages` — do not include the Microsoft `Base Application`, `System Application`, `Application`, `Business Foundation`, or `System` apps.
3. Initialize an AL LSP session with `expectedProjectReferenceDefinitions: []` so the closure-loaded check returns true. (This is the workaround the community uses to avoid the OTHER known hang where `al/hasProjectClosureLoadedRequest` would otherwise return false forever.)
4. Send any `al/symbolSearch` request:

```json
{ "jsonrpc": "2.0", "id": 1, "method": "al/symbolSearch", "params": { "query": "Approvals Mgmt." } }
```

**Expected:** Either a result, an empty result, or a JSON-RPC error response.
**Actual:** No response. The request hangs forever.

## Root cause

AL LSP throws an unhandled `NullReferenceException` while building the symbol descriptor for a key (`ReferenceKeySymbol`) whose referenced `ContainerSymbol` (the Table) is unavailable because the dependency app that defines it isn't in `packageCachePaths`.

The exception IS logged through `window/logMessage` (visible in VS Code's "AL Language Extension" output channel and in `bin/win32/EditorServices.log` when `/logLevel:Verbose` is set):

```
[Error] Processing of message 'al/symbolSearch' failed with error: 'Object reference not set to an instance of an object.'
Details:
System.NullReferenceException: Object reference not set to an instance of an object.
   at Microsoft.Dynamics.Nav.CodeAnalysis.Symbols.ReferenceKeySymbol.AppendFieldMembers(PooledNameObjectDictionary`1 fields, ContainerSymbol table)
     in X:\source\Prod\Microsoft.Dynamics.Nav.CodeAnalysis\Symbols\Reference\ReferenceKeySymbol.cs:line 83
   at Microsoft.Dynamics.Nav.CodeAnalysis.Symbols.ReferenceKeySymbol.GetLazyFields()
     in ReferenceKeySymbol.cs:line 60
   at System.Lazy`1.PublicationOnlyViaFactory(LazyHelper initializer)
   at System.Lazy`1.CreateValue()
   at System.Lazy`1.get_Value()
   at Microsoft.Dynamics.Nav.CodeAnalysis.Symbols.ReferenceKeySymbol.get_Fields()
     in ReferenceKeySymbol.cs:line 44
   at Microsoft.Dynamics.Nav.CodeAnalysis.SymbolDisplayVisitor.VisitKey(KeySymbol symbol)
     in SymbolDisplay\SymbolDisplayVisitor.cs:line 897
   at Microsoft.Dynamics.Nav.CodeAnalysis.SymbolDisplay.ToDisplayParts(...)
     in SymbolDisplay\SymbolDisplay.cs:line 125
   at Microsoft.Dynamics.Nav.CodeAnalysis.Symbols.Symbol.ToDisplayString(...)
     in Symbols\Symbol.cs:line 529
   at Microsoft.Dynamics.Nav.CodeAnalysis.Workspaces.LanguageModelTools.SymbolSearch.SymbolSearchService.SymbolDescriptor.Create(Symbol, Symbol, SymbolSearchScope, Boolean, CancellationToken)
     in LanguageModelTools\SymbolSearch\SymbolSearchService.cs:line 606
   at SymbolSearchService.AppendMembers(...)
     in SymbolSearchService.cs:line 202
   at SymbolSearchService.BuildDescriptors(...)
     in SymbolSearchService.cs:line 184
   at SymbolSearchService.GetDependencyDescriptorsAsync(...)
     in SymbolSearchService.cs:line 152
   at SymbolSearchService.SearchAsync(ProjectId, SymbolSearchParameters, CancellationToken)
     in SymbolSearchService.cs:line 76
   at Microsoft.Dynamics.Nav.EditorServices.Protocol.LanguageServer.Extensions.SymbolSearchRequestHandler.HandleAsync(SymbolSearchRequest, Int32, CancellationToken)
     in LanguageServer\Extensions\Tools\SymbolSearchRequestHandler.cs:line 39
   at Microsoft.Dynamics.Nav.EditorServices.Protocol.MessageProtocol.RequestHandlerBase`1.HandleAsync(JToken, Int32, CancellationToken)
     in MessageProtocol\RequestHandlerBase.cs:line 85
   at Microsoft.Dynamics.Nav.EditorServices.Protocol.RequestRegistry.Process(Message message)
     in Endpoints\RequestRegistry.cs:line 84
```

Two problems compounded:

1. **`ReferenceKeySymbol.AppendFieldMembers` does not null-check `table`** before dereferencing it. A key targeting a table that isn't in the loaded closure trips this consistently.
2. **`SymbolSearchRequestHandler.HandleAsync` does not catch exceptions** thrown by `SearchAsync` and translate them into JSON-RPC error responses. The exception bubbles into the request loop's general handler (which logs it) but no error reply is sent to the client.

Either fix would prevent the user-visible silent hang. Both together would be defense-in-depth.

## Suggested fix

In `ReferenceKeySymbol.AppendFieldMembers` (`X:\source\Prod\...\Symbols\Reference\ReferenceKeySymbol.cs:83`):

```csharp
if (table == null) {
    return;  // or: throw a specific "missing dependency" exception
}
```

In `SymbolSearchRequestHandler.HandleAsync` (`X:\source\Prod\...\LanguageServer\Extensions\Tools\SymbolSearchRequestHandler.cs:39`):

```csharp
try {
    return await this.searchService.SearchAsync(...);
} catch (Exception ex) {
    // Translate to JSON-RPC error so the client sees a response
    throw new ResponseError(ErrorCodes.InternalError, ex.Message, ex);
}
```

## Severity

High for tools that drive AL LSP programmatically (LSP-based agents, MCP integrations, CI symbol indexers). The user-visible symptom is "indefinite hang on first symbol search" — no error, no timeout, no log surface in editors that don't expose `window/logMessage` to the user.

## Repro environment

- Windows 11 24H2
- AL extension `ms-dynamics-smb.al-18.0.2293710`
- Editor Services Host v18.0.35.20
- .NET runtime bundled with the extension
- Project: a minimal BC22+ project with `app.json` declaring a non-Microsoft dependency, and `.alpackages` containing only that dependency.

## Independent reproduction script

See `test-al-project/probe_al_lsp_silence.py` in the al-lsp-for-agents repo. Scenario F against the DocumentOutput/Cloud project triggers the hang reliably; scenario G demonstrates the workaround (adding ancestor `.alpackages` to `packageCachePaths`).
