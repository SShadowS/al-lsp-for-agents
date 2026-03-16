import * as vscode from "vscode";
import { LanguageClient } from "vscode-languageclient/node";

/**
 * Registers Language Model Tools that Copilot agent mode can invoke.
 * Each tool wraps an LSP request through the LanguageClient.
 */
export function registerTools(
  context: vscode.ExtensionContext,
  client: LanguageClient
): void {
  context.subscriptions.push(
    vscode.lm.registerTool(
      "bclsp_goToDefinition",
      new GoToDefinitionTool(client)
    )
  );

  context.subscriptions.push(
    vscode.lm.registerTool("bclsp_hover", new HoverTool(client))
  );

  context.subscriptions.push(
    vscode.lm.registerTool(
      "bclsp_findReferences",
      new FindReferencesTool(client)
    )
  );

  context.subscriptions.push(
    vscode.lm.registerTool(
      "bclsp_prepareCallHierarchy",
      new PrepareCallHierarchyTool(client)
    )
  );

  context.subscriptions.push(
    vscode.lm.registerTool(
      "bclsp_incomingCalls",
      new IncomingCallsTool(client)
    )
  );

  context.subscriptions.push(
    vscode.lm.registerTool(
      "bclsp_outgoingCalls",
      new OutgoingCallsTool(client)
    )
  );

  context.subscriptions.push(
    vscode.lm.registerTool("bclsp_codeLens", new CodeLensTool(client))
  );

  context.subscriptions.push(
    vscode.lm.registerTool(
      "bclsp_codeQualityDiagnostics",
      new CodeQualityDiagnosticsTool(client)
    )
  );

  context.subscriptions.push(
    vscode.lm.registerTool(
      "bclsp_documentSymbols",
      new DocumentSymbolsTool(client)
    )
  );

  context.subscriptions.push(
    vscode.lm.registerTool(
      "bclsp_workspaceSymbols",
      new WorkspaceSymbolsTool(client)
    )
  );

  context.subscriptions.push(
    vscode.lm.registerTool(
      "bclsp_renameSymbol",
      new RenameSymbolTool(client)
    )
  );

  context.subscriptions.push(
    vscode.lm.registerTool(
      "bclsp_symbolSearch",
      new SymbolSearchTool(client)
    )
  );
}

// --- Tool Implementations ---

interface PositionInput {
  uri: string;
  line: number;
  character: number;
}

interface UriInput {
  uri?: string;
}

interface QueryInput {
  query: string;
}

interface RenameInput {
  uri: string;
  line: number;
  character: number;
  newName: string;
}

interface SymbolSearchInput {
  query: string;
  filters?: {
    kinds?: string[];
    namespace?: string;
    objectName?: string;
    memberKinds?: string[];
    scope?: string;
    limit?: number;
  };
}

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

class GoToDefinitionTool implements vscode.LanguageModelTool<PositionInput> {
  constructor(private client: LanguageClient) {}

  async invoke(
    options: vscode.LanguageModelToolInvocationOptions<PositionInput>,
    _token: vscode.CancellationToken
  ): Promise<vscode.LanguageModelToolResult> {
    const { uri, line, character } = options.input;
    const locations = await this.client.sendRequest<
      { uri: string; range: Range }[] | { uri: string; range: Range } | null
    >("textDocument/definition", {
      textDocument: { uri },
      position: { line, character },
    });

    if (!locations) {
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart("No definition found."),
      ]);
    }

    const locs = Array.isArray(locations) ? locations : [locations];
    const text = locs
      .map((loc) => `${loc.uri} line ${loc.range.start.line + 1}`)
      .join("\n");

    return new vscode.LanguageModelToolResult([
      new vscode.LanguageModelTextPart(text),
    ]);
  }
}

class HoverTool implements vscode.LanguageModelTool<PositionInput> {
  constructor(private client: LanguageClient) {}

  async invoke(
    options: vscode.LanguageModelToolInvocationOptions<PositionInput>,
    _token: vscode.CancellationToken
  ): Promise<vscode.LanguageModelToolResult> {
    const { uri, line, character } = options.input;
    const hover = await this.client.sendRequest<{
      contents: { kind: string; value: string } | string;
    } | null>("textDocument/hover", {
      textDocument: { uri },
      position: { line, character },
    });

    if (!hover) {
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart("No hover information available."),
      ]);
    }

    // The Go wrapper already enriches hover with full XML doc comments
    const content =
      typeof hover.contents === "string"
        ? hover.contents
        : hover.contents.value;

    return new vscode.LanguageModelToolResult([
      new vscode.LanguageModelTextPart(content),
    ]);
  }
}

class FindReferencesTool implements vscode.LanguageModelTool<PositionInput> {
  constructor(private client: LanguageClient) {}

  async invoke(
    options: vscode.LanguageModelToolInvocationOptions<PositionInput>,
    _token: vscode.CancellationToken
  ): Promise<vscode.LanguageModelToolResult> {
    const { uri, line, character } = options.input;
    const refs = await this.client.sendRequest<
      { uri: string; range: Range }[] | null
    >("textDocument/references", {
      textDocument: { uri },
      position: { line, character },
      context: { includeDeclaration: true },
    });

    if (!refs || refs.length === 0) {
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart("No references found."),
      ]);
    }

    const text = refs
      .map((ref) => `${ref.uri} line ${ref.range.start.line + 1}`)
      .join("\n");

    return new vscode.LanguageModelToolResult([
      new vscode.LanguageModelTextPart(`Found ${refs.length} references:\n${text}`),
    ]);
  }
}

class PrepareCallHierarchyTool
  implements vscode.LanguageModelTool<PositionInput>
{
  constructor(private client: LanguageClient) {}

  async invoke(
    options: vscode.LanguageModelToolInvocationOptions<PositionInput>,
    _token: vscode.CancellationToken
  ): Promise<vscode.LanguageModelToolResult> {
    const { uri, line, character } = options.input;
    const items = await this.client.sendRequest<CallHierarchyItem[] | null>(
      "textDocument/prepareCallHierarchy",
      {
        textDocument: { uri },
        position: { line, character },
      }
    );

    if (!items || items.length === 0) {
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart(
          "No call hierarchy item at this position."
        ),
      ]);
    }

    const text = items
      .map(
        (item) =>
          `${item.name} (${item.uri} line ${item.range.start.line + 1})`
      )
      .join("\n");

    return new vscode.LanguageModelToolResult([
      new vscode.LanguageModelTextPart(text),
    ]);
  }
}

class IncomingCallsTool implements vscode.LanguageModelTool<PositionInput> {
  constructor(private client: LanguageClient) {}

  async invoke(
    options: vscode.LanguageModelToolInvocationOptions<PositionInput>,
    _token: vscode.CancellationToken
  ): Promise<vscode.LanguageModelToolResult> {
    const { uri, line, character } = options.input;

    // Step 1: Prepare call hierarchy
    const items = await this.client.sendRequest<CallHierarchyItem[] | null>(
      "textDocument/prepareCallHierarchy",
      {
        textDocument: { uri },
        position: { line, character },
      }
    );

    if (!items || items.length === 0) {
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart(
          "No call hierarchy item at this position."
        ),
      ]);
    }

    // Step 2: Get incoming calls
    const calls = await this.client.sendRequest<IncomingCall[] | null>(
      "callHierarchy/incomingCalls",
      { item: items[0] }
    );

    if (!calls || calls.length === 0) {
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart(
          `No incoming calls found for ${items[0].name}.`
        ),
      ]);
    }

    const text = calls
      .map(
        (call) =>
          `${call.from.name} (${call.from.uri} line ${call.from.range.start.line + 1})`
      )
      .join("\n");

    return new vscode.LanguageModelToolResult([
      new vscode.LanguageModelTextPart(
        `Found ${calls.length} callers of ${items[0].name}:\n${text}`
      ),
    ]);
  }
}

class OutgoingCallsTool implements vscode.LanguageModelTool<PositionInput> {
  constructor(private client: LanguageClient) {}

  async invoke(
    options: vscode.LanguageModelToolInvocationOptions<PositionInput>,
    _token: vscode.CancellationToken
  ): Promise<vscode.LanguageModelToolResult> {
    const { uri, line, character } = options.input;

    const items = await this.client.sendRequest<CallHierarchyItem[] | null>(
      "textDocument/prepareCallHierarchy",
      {
        textDocument: { uri },
        position: { line, character },
      }
    );

    if (!items || items.length === 0) {
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart(
          "No call hierarchy item at this position."
        ),
      ]);
    }

    const calls = await this.client.sendRequest<OutgoingCall[] | null>(
      "callHierarchy/outgoingCalls",
      { item: items[0] }
    );

    if (!calls || calls.length === 0) {
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart(
          `No outgoing calls found from ${items[0].name}.`
        ),
      ]);
    }

    const text = calls
      .map(
        (call) =>
          `${call.to.name} (${call.to.uri} line ${call.to.range.start.line + 1})`
      )
      .join("\n");

    return new vscode.LanguageModelToolResult([
      new vscode.LanguageModelTextPart(
        `Found ${calls.length} callees from ${items[0].name}:\n${text}`
      ),
    ]);
  }
}

class CodeLensTool implements vscode.LanguageModelTool<UriInput> {
  constructor(private client: LanguageClient) {}

  async invoke(
    options: vscode.LanguageModelToolInvocationOptions<UriInput>,
    _token: vscode.CancellationToken
  ): Promise<vscode.LanguageModelToolResult> {
    const { uri } = options.input;
    const lenses = await this.client.sendRequest<CodeLens[] | null>(
      "textDocument/codeLens",
      { textDocument: { uri } }
    );

    if (!lenses || lenses.length === 0) {
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart("No code lens items found."),
      ]);
    }

    const text = lenses
      .map(
        (lens) =>
          `Line ${lens.range.start.line + 1}: ${lens.command?.title ?? "unknown"}`
      )
      .join("\n");

    return new vscode.LanguageModelToolResult([
      new vscode.LanguageModelTextPart(text),
    ]);
  }
}

class CodeQualityDiagnosticsTool
  implements vscode.LanguageModelTool<UriInput>
{
  constructor(private _client: LanguageClient) {}

  async invoke(
    options: vscode.LanguageModelToolInvocationOptions<UriInput>,
    _token: vscode.CancellationToken
  ): Promise<vscode.LanguageModelToolResult> {
    const { uri } = options.input;

    // Read diagnostics from VS Code's diagnostics collection
    // al-call-hierarchy pushes diagnostics via textDocument/publishDiagnostics
    // which the LanguageClient automatically adds to VS Code's diagnostics
    const targetUri = uri ? vscode.Uri.parse(uri) : undefined;
    const allDiagnostics = vscode.languages.getDiagnostics();

    const qualityDiagnostics: string[] = [];
    for (const [docUri, diagnostics] of allDiagnostics) {
      if (targetUri && docUri.toString() !== targetUri.toString()) {
        continue;
      }
      for (const diag of diagnostics) {
        if (diag.source === "al-call-hierarchy") {
          qualityDiagnostics.push(
            `${docUri.fsPath} line ${diag.range.start.line + 1}: [${severityToString(diag.severity)}] ${diag.message}`
          );
        }
      }
    }

    if (qualityDiagnostics.length === 0) {
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart(
          "No code quality diagnostics found."
        ),
      ]);
    }

    return new vscode.LanguageModelToolResult([
      new vscode.LanguageModelTextPart(
        `Found ${qualityDiagnostics.length} code quality issues:\n${qualityDiagnostics.join("\n")}`
      ),
    ]);
  }
}

function severityToString(severity: vscode.DiagnosticSeverity): string {
  switch (severity) {
    case vscode.DiagnosticSeverity.Error:
      return "Error";
    case vscode.DiagnosticSeverity.Warning:
      return "Warning";
    case vscode.DiagnosticSeverity.Information:
      return "Info";
    case vscode.DiagnosticSeverity.Hint:
      return "Hint";
    default:
      return "Unknown";
  }
}

class DocumentSymbolsTool implements vscode.LanguageModelTool<UriInput> {
  constructor(private client: LanguageClient) {}

  async invoke(
    options: vscode.LanguageModelToolInvocationOptions<UriInput>,
    _token: vscode.CancellationToken
  ): Promise<vscode.LanguageModelToolResult> {
    const { uri } = options.input;
    if (!uri) {
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart("A file URI is required."),
      ]);
    }

    const symbols = await this.client.sendRequest<DocumentSymbol[] | null>(
      "textDocument/documentSymbol",
      { textDocument: { uri } }
    );

    if (!symbols || symbols.length === 0) {
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart("No symbols found in this file."),
      ]);
    }

    return new vscode.LanguageModelToolResult([
      new vscode.LanguageModelTextPart(formatSymbolTree(symbols)),
    ]);
  }
}

class WorkspaceSymbolsTool implements vscode.LanguageModelTool<QueryInput> {
  constructor(private client: LanguageClient) {}

  async invoke(
    options: vscode.LanguageModelToolInvocationOptions<QueryInput>,
    _token: vscode.CancellationToken
  ): Promise<vscode.LanguageModelToolResult> {
    const { query } = options.input;
    const symbols = await this.client.sendRequest<SymbolInformation[] | null>(
      "workspace/symbol",
      { query }
    );

    if (!symbols || symbols.length === 0) {
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart("No symbols found matching the query."),
      ]);
    }

    const text = symbols
      .map((sym) => {
        const container = sym.containerName ? ` in ${sym.containerName}` : "";
        return `${sym.name} (${symbolKindToString(sym.kind)})${container} - ${sym.location.uri}`;
      })
      .join("\n");

    return new vscode.LanguageModelToolResult([
      new vscode.LanguageModelTextPart(`Found ${symbols.length} symbols:\n${text}`),
    ]);
  }
}

class RenameSymbolTool implements vscode.LanguageModelTool<RenameInput> {
  constructor(private client: LanguageClient) {}

  async invoke(
    options: vscode.LanguageModelToolInvocationOptions<RenameInput>,
    _token: vscode.CancellationToken
  ): Promise<vscode.LanguageModelToolResult> {
    const { uri, line, character, newName } = options.input;

    let lspEdit: object | null;
    try {
      lspEdit = await this.client.sendRequest<object | null>(
        "textDocument/rename",
        {
          textDocument: { uri },
          position: { line, character },
          newName,
        }
      );
    } catch (err: unknown) {
      const message =
        err instanceof Error ? err.message : "Unknown error";
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart(
          `Rename failed: ${message}`
        ),
      ]);
    }

    if (!lspEdit) {
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart(
          "Rename could not be performed at this position."
        ),
      ]);
    }

    const workspaceEdit =
      await this.client.protocol2CodeConverter.asWorkspaceEdit(lspEdit as any);

    if (!workspaceEdit) {
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart(
          "Rename could not be performed at this position."
        ),
      ]);
    }

    const success = await vscode.workspace.applyEdit(workspaceEdit);

    if (!success) {
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart("Failed to apply rename edits."),
      ]);
    }

    // Build summary from raw LSP edit (before conversion)
    const rawEdit = lspEdit as { changes?: Record<string, unknown[]>; documentChanges?: unknown[] };
    let fileCount = 0;
    let totalEdits = 0;
    const fileLines: string[] = [];
    if (rawEdit.changes) {
      for (const [fileUri, edits] of Object.entries(rawEdit.changes)) {
        fileCount++;
        totalEdits += edits.length;
        fileLines.push(`  ${fileUri} (${edits.length} edits)`);
      }
    } else if (rawEdit.documentChanges) {
      fileCount = rawEdit.documentChanges.length;
      totalEdits = fileCount;
    }

    return new vscode.LanguageModelToolResult([
      new vscode.LanguageModelTextPart(
        `Renamed to "${newName}" across ${fileCount} files (${totalEdits} edits):\n${fileLines.join("\n")}`
      ),
    ]);
  }
}

class SymbolSearchTool implements vscode.LanguageModelTool<SymbolSearchInput> {
  constructor(private client: LanguageClient) {}

  async invoke(
    options: vscode.LanguageModelToolInvocationOptions<SymbolSearchInput>,
    _token: vscode.CancellationToken
  ): Promise<vscode.LanguageModelToolResult> {
    const { query, filters } = options.input;

    const result = await this.client.sendRequest<ALSymbolSearchResult | null>(
      "al/symbolSearch",
      { query, filters: filters ?? {} }
    );

    if (!result || !result.symbols || result.symbols.length === 0) {
      return new vscode.LanguageModelToolResult([
        new vscode.LanguageModelTextPart("No symbols found matching the query."),
      ]);
    }

    const text = result.symbols
      .map((sym) => {
        const container = sym.containerName ? ` in ${sym.containerName}` : "";
        const sig = sym.signature ? ` — ${sym.signature}` : "";
        const path = sym.path ? ` - ${sym.path}` : "";
        return `${sym.name} (${sym.kind})${container}${sig}${path}`;
      })
      .join("\n");

    const truncNote = result.truncated ? " (truncated)" : "";

    return new vscode.LanguageModelToolResult([
      new vscode.LanguageModelTextPart(
        `Found ${result.symbols.length} symbols${truncNote}:\n${text}`
      ),
    ]);
  }
}

// --- LSP Types (subset needed for tool implementations) ---

interface Range {
  start: { line: number; character: number };
  end: { line: number; character: number };
}

interface CallHierarchyItem {
  name: string;
  kind: number;
  uri: string;
  range: Range;
  selectionRange: Range;
}

interface IncomingCall {
  from: CallHierarchyItem;
  fromRanges: Range[];
}

interface OutgoingCall {
  to: CallHierarchyItem;
  fromRanges: Range[];
}

interface CodeLens {
  range: Range;
  command?: {
    title: string;
    command: string;
    arguments?: unknown[];
  };
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

interface ALSymbolSearchResult {
  symbols: Array<{
    name: string;
    fullName?: string;
    kind: string;
    containerName?: string;
    signature?: string;
    path?: string;
  }>;
  truncated: boolean;
}
