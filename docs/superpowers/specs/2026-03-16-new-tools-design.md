# Add documentSymbols, workspaceSymbols, and renameSymbol Tools — Design Spec

## Goal

Add three new VS Code Language Model Tools (`bclsp_documentSymbols`, `bclsp_workspaceSymbols`, `bclsp_renameSymbol`) to eliminate the need for the generic VSC-as-MCP extension in AL development workflows.

## Architecture

All three tools follow the established pattern: TypeScript classes in `tools.ts` using `client.sendRequest()` through the LanguageClient to the Go wrapper, which forwards to the AL Language Server. No Go wrapper or Rust changes needed.

## Tools

### bclsp_documentSymbols

**LSP method:** `textDocument/documentSymbol`
**Input type:** `UriInput` (`{ uri: string }`) — same as `bclsp_codeLens`, `uri` is optional in `UriInput` but required in the JSON schema
**Response type:** `DocumentSymbol[]` (hierarchical tree with children)

**Output formatting:** Render as indented tree showing hierarchy. AL files have objects (codeunit, table, page) containing members (procedures, fields, actions). Example:

```
Codeunit 50000 CustomerMgt (Class) - Line 1
  OnRun() (Function) - Line 5
  CreateCustomer(CustomerNo: Code[20], CustomerName: Text[100]): Boolean (Function) - Line 22
  var (Property) - Line 3
    Customer: Record Customer (Variable) - Line 4
```

Use `SymbolKind` numeric values mapped to readable names.

### bclsp_workspaceSymbols

**LSP method:** `workspace/symbol`
**Input type:** New `QueryInput` (`{ query: string }`)
**Response type:** `SymbolInformation[]` (flat list with locations)

**Output formatting:** Flat list with file and container name. The Go wrapper's `WorkspaceSymbolHandler` always returns line 0 because the underlying `al/symbolSearch` API doesn't provide position data, so line numbers are omitted from output. Include `containerName` when present to disambiguate symbols. Example:

```
CustomerMgt (Class) - file:///path/to/CustomerMgt.al
CreateCustomer (Function) in CustomerMgt - file:///path/to/CustomerMgt.al
```

### bclsp_renameSymbol

**LSP method:** `textDocument/rename`
**Input type:** Extended `PositionInput` with `newName` (`{ uri, line, character, newName }`)
**Response type:** `WorkspaceEdit` (LSP protocol type)

**Behavior:**
1. Send `textDocument/rename` request via LanguageClient
2. Convert the raw LSP `WorkspaceEdit` to a `vscode.WorkspaceEdit` using `client.protocol2CodeConverter.asWorkspaceEdit()`
3. Apply via `vscode.workspace.applyEdit()`
4. Report what changed, listing affected files

Example output:

```
Renamed to "CreateOrUpdateCustomer" across 3 files (7 edits):
  file:///path/to/CustomerMgt.al (3 edits)
  file:///path/to/SalesOrder.al (2 edits)
  file:///path/to/Tests.al (2 edits)
```

**Error handling:**
- If the server returns null/empty: "Rename could not be performed at this position."
- If the server returns an error (e.g., symbol from dependency, built-in type): report the error message from the server.
- If `applyEdit` returns false: "Failed to apply rename edits."

## Implementation Details

### New types needed in tools.ts

```typescript
interface QueryInput {
  query: string;
}

interface RenameInput {
  uri: string;
  line: number;
  character: number;
  newName: string;
}

interface DocumentSymbol {
  name: string;
  detail?: string;
  kind: number;
  range: Range;
  selectionRange: Range;
  children?: DocumentSymbol[];
}

interface SymbolInformation {
  name: string;
  kind: number;
  location: { uri: string; range: Range };
  containerName?: string;
}
```

### SymbolKind mapping

Shared helper used by both `bclsp_documentSymbols` and `bclsp_workspaceSymbols`:

```typescript
function symbolKindToString(kind: number): string {
  const kinds: Record<number, string> = {
    1: "File", 2: "Module", 3: "Namespace", 4: "Package",
    5: "Class", 6: "Method", 7: "Property", 8: "Field",
    9: "Constructor", 10: "Enum", 11: "Interface", 12: "Function",
    13: "Variable", 14: "Constant", 15: "String", 16: "Number",
    17: "Boolean", 18: "Array", 19: "Object", 20: "Key",
    21: "Null", 22: "EnumMember", 23: "Struct", 24: "Event",
    25: "Operator", 26: "TypeParameter",
  };
  return kinds[kind] ?? "Symbol";
}
```

### Hierarchical rendering for documentSymbols

```typescript
function formatSymbolTree(symbols: DocumentSymbol[], indent: number = 0): string {
  const lines: string[] = [];
  for (const sym of symbols) {
    const prefix = "  ".repeat(indent);
    const detail = sym.detail ? ` (${sym.detail})` : "";
    lines.push(
      `${prefix}${sym.name}${detail} (${symbolKindToString(sym.kind)}) - Line ${sym.range.start.line + 1}`
    );
    if (sym.children && sym.children.length > 0) {
      lines.push(formatSymbolTree(sym.children, indent + 1));
    }
  }
  return lines.join("\n");
}
```

### Rename: WorkspaceEdit conversion and summary

The raw LSP response must be converted before applying:

```typescript
const workspaceEdit = await client.protocol2CodeConverter.asWorkspaceEdit(lspEdit);
const success = await vscode.workspace.applyEdit(workspaceEdit);
```

For the summary, iterate `workspaceEdit.entries()` to count files and edits.

## package.json Entries

Three new entries in `contributes.languageModelTools` with full `modelDescription` fields:

- `bclsp_documentSymbols`: `modelDescription` — "Get the symbol tree for an AL file. Returns all objects, procedures, fields, actions, and variables with their hierarchy. Use this to understand file structure before reading or editing."
  - tags: `["al", "symbols", "structure"]`, requires `uri`

- `bclsp_workspaceSymbols`: `modelDescription` — "Search for AL symbols across the workspace by name. Returns matching tables, codeunits, pages, procedures, and other symbols with their file locations. Use this to find where a symbol is defined without knowing its file."
  - tags: `["al", "symbols", "search"]`, requires `query`

- `bclsp_renameSymbol`: `modelDescription` — "Rename an AL symbol at a given position, applying changes across all files in the workspace. Provide a file URI, position on the symbol, and the new name. Use this for safe refactoring of procedure names, variable names, and other symbols."
  - tags: `["al", "refactoring", "rename"]`, requires `uri`, `line`, `character`, `newName`

## Files Changed

| File | Change |
|------|--------|
| `vscode-extension/src/tools.ts` | Add 3 tool classes, 2 input types, helper functions, 3 registrations |
| `vscode-extension/package.json` | Add 3 `languageModelTools` entries |
| `vscode-extension/README.md` | Add 3 rows to feature table |

## Testing

Manual testing in VS Code with an AL project:
1. `bclsp_documentSymbols` on a codeunit file — verify hierarchical output
2. `bclsp_workspaceSymbols` with query "Customer" — verify results from multiple files
3. `bclsp_renameSymbol` on a procedure name — verify edits applied across files, summary lists affected files
