# Use LSP for AL Code - Not Grep

For `.al` files, **use the LSP tool instead of Grep/Glob/Read** for anything about code structure, symbols, types, or navigation. LSP understands AL semantics. Grep matches text.

## When to use LSP (not Grep)

- "Where is X defined?" -> `goToDefinition`, not Grep
- "Who calls X?" -> `incomingCalls`, not Grep
- "What calls does X make?" -> `outgoingCalls`, not Grep
- "Where is X used?" -> `findReferences`, not Grep
- "What type is X? What fields does this table have?" -> `hover`, not Read
- "What's in this file?" -> `documentSymbol`, not Read
- "Find a symbol by name" -> `workspaceSymbol` with a `query`, not Grep (needs Claude Code 2.1.x+)
- "What extends/implements X? What's X's source table?" -> `symbolRelations`
- "What's on this page? page layout/actions?" -> `inspectPage`
- "How many references? Code quality?" -> `codeLens`

## When Grep/Glob is fine

- Searching comments, TODOs, string literals
- Finding files by name pattern
- Searching non-AL files

## When LSP returns nothing

1. Check the file path exists (most common cause)
2. Try Serena tools as fallback (`find_symbol`, `get_symbols_overview`)
3. Then fall back to Grep

## AL-specific

- Lines and characters are **1-based** in LSP results
- Files must be in an AL project (has `app.json`)
- `hover` on a Record/Table returns all fields with types
- `documentSymbol` returns object IDs, enum ordinals, page structure
