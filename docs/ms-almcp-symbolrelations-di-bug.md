# Bug report (DRAFT): `al_symbolrelations` MCP tool fails — `SymbolRelationsService` not registered in DI

Status: DRAFT for review. Not yet submitted. Prepared 2026-06-16.

## Summary

The `al_symbolrelations` tool exposed by the AL MCP server (`al launchmcpserver` / `almcp`) throws an unhandled `InvalidOperationException` on every invocation because its required service `SymbolRelationsService` is not registered in the DI container. The sibling tool `al_inspectpage` works correctly, which isolates the defect to the symbol-relations service registration specifically.

## Environment

- AL build tools (nuget `microsoft.dynamics.businesscentral.development.tools`): **18.0.37.11445-beta** (also reproduces on the `almcp` bundled in VS Code AL extension `ms-dynamics-smb.al-18.0.2293710`).
- Transport: stdio (`--transport stdio`).
- OS: Windows 11.

## Reproduction

1. Launch the MCP server over stdio against any AL project that has symbols available:

   ```
   al launchmcpserver <projectDir> --packagecachepath <projectDir>\.alpackages --transport stdio --nolog
   ```

2. MCP `initialize` + `notifications/initialized` handshake (protocolVersion `2024-11-05`).

3. Call the tool:

   ```json
   {"jsonrpc":"2.0","id":2,"method":"tools/call",
    "params":{"name":"al_symbolrelations",
              "arguments":{"parameters":{"symbolName":"Customer","symbolKind":"Table"}}}}
   ```

## Expected

A `ToolCallResult` with the symbol's relations (SourceTable / Extends / Implements / ExtendedBy / …), as documented in the tool's own description.

## Actual

```json
{"jsonrpc":"2.0","id":2,
 "result":{"content":[{"type":"text","text":"An error occurred invoking 'al_symbolrelations'."}],
           "isError":true}}
```

Server-side log shows:

```
System.InvalidOperationException: No service for type
'Microsoft.Dynamics.Nav.CodeAnalysis.Workspaces.LanguageModelTools.SymbolRelations.SymbolRelationsService'
has been registered.
```

## Root cause (from decompiled `almcp.dll`)

The DI wiring registers the **tool service** but expects an **unregistered dependency**:

```csharp
// SymbolRelationsToolService is registered, and it requires SymbolRelationsService:
services.AddSingleton<ISymbolRelationsToolService>(sp =>
    new SymbolRelationsToolService(
        sp.GetRequiredService<CompilationService>(),
        sp.GetRequiredService<SymbolRelationsService>()));   // <-- SymbolRelationsService never registered
```

Contrast with the working `al_inspectpage`, whose service IS registered:

```csharp
services.AddSingleton<IInspectPageToolService>(sp =>
    new InspectPageToolService(
        sp.GetRequiredService<CompilationService>(),
        sp.GetRequiredService<InspectPageService>()));       // InspectPageService registered just above
```

`GetRequiredService<SymbolRelationsService>()` throws because nothing ever did `services.AddSingleton<SymbolRelationsService>(...)` (or equivalent) the way `InspectPageService` is added from the workspace.

## Suggested fix

Register `SymbolRelationsService` in the MCP server's service collection (mirroring `InspectPageService`) before constructing `SymbolRelationsToolService`.

## Note

The same capability exposed as the EditorServices LSP custom method `al/symbolRelations` (used by the VS Code AL extension) is **not** affected — it returns a well-formed `{relations, truncated}` payload. Only the MCP tool path is broken.
