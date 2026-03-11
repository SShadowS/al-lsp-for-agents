# AL LSP for Agents

Code intelligence for AL (Microsoft Dynamics 365 Business Central) that works with **GitHub Copilot agent mode** and **Claude Code**.

This extension gives AI agents the same code navigation tools that developers use — go to definition, find references, call hierarchy, and code quality diagnostics — so they can understand and modify AL code with full semantic awareness.

## Features

### Language Model Tools for Copilot

When using GitHub Copilot in agent mode, these tools become available:

| Tool | Description |
|------|-------------|
| `al_goToDefinition` | Navigate to where a symbol is defined |
| `al_hover` | Get type info, signatures, field lists, and declared properties |
| `al_findReferences` | Find all usages of a symbol across the project |
| `al_prepareCallHierarchy` | Get call hierarchy item at a cursor position |
| `al_incomingCalls` | Find all callers of a procedure |
| `al_outgoingCalls` | Find all procedures called by a procedure |
| `al_codeLens` | Get reference counts and quality metrics for procedures |
| `al_codeQualityDiagnostics` | Unused procedures, high complexity, long methods, etc. |

### Enriched Hover

Hover results are automatically enriched with:

- **XML doc comments** from source code (summaries, parameter descriptions, return values)
- **Field properties** from table definitions (Caption, FieldClass, CalcFormula, TableRelation, Editable, etc.)
- **Action properties** from page definitions (RunObject, ToolTip, Image, ShortcutKey, etc.)

All declared properties are extracted — if a property isn't shown, it genuinely isn't set in the source code.

### Code Quality Diagnostics

Real-time diagnostics powered by [al-call-hierarchy](https://github.com/SShadowS/al-call-hierarchy):

- Unused procedures (no callers)
- High cyclomatic complexity
- Too many parameters
- High fan-in (many callers)
- Long methods

### Configurable Thresholds

Diagnostic thresholds are configurable at two levels. All values are optional — missing values use sensible defaults.

#### Global Defaults

Set defaults for all your projects in `~/.al-call-hierarchy/config.json`:

```json
{
  "diagnostics": {
    "complexity": { "warning": 8, "critical": 15 },
    "unusedProcedures": false
  }
}
```

#### Per-Workspace Overrides

Override for a specific project in `{workspace}/.al-call-hierarchy.json`:

```json
{
  "diagnostics": {
    "complexity": { "enabled": true, "warning": 8, "critical": 15 },
    "parameters": { "enabled": false },
    "lineCount": { "warning": 30, "critical": 80 },
    "fanIn": { "enabled": false },
    "unusedProcedures": false
  }
}
```

Config is merged per field: **built-in defaults → global → workspace**. A workspace config only needs to specify fields it wants to override. Set `"enabled": false` on any category to disable it entirely.

### Call Hierarchy

Full incoming and outgoing call analysis for procedures, including cross-file navigation and event subscriber detection.

## Prerequisites

- **VS Code** with the [AL Language extension](https://marketplace.visualstudio.com/items?itemName=ms-dynamics-smb.al) installed
- An AL project with `app.json` and downloaded symbols (`.alpackages`)

The extension bundles all required binaries — no additional installation needed.

## How It Works

The extension starts a Go wrapper that combines:

1. **Microsoft AL Language Server** — compiler-powered definitions, hover, references, and diagnostics
2. **al-call-hierarchy** — tree-sitter-powered call hierarchy, code lens, code quality diagnostics, and property extraction

The wrapper merges capabilities from both servers and presents a single enriched LSP interface.

## Also Available for Claude Code

This project also provides Claude Code plugins for the same AL code intelligence:

```
/plugin marketplace add SShadowS/al-lsp-for-agents
```

See the [GitHub repo](https://github.com/SShadowS/al-lsp-for-agents) for details.

## License

[MIT](https://opensource.org/licenses/MIT)
