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
      "al_goToDefinition",
      new GoToDefinitionTool(client)
    )
  );

  context.subscriptions.push(
    vscode.lm.registerTool("al_hover", new HoverTool(client))
  );

  context.subscriptions.push(
    vscode.lm.registerTool(
      "al_findReferences",
      new FindReferencesTool(client)
    )
  );

  context.subscriptions.push(
    vscode.lm.registerTool(
      "al_prepareCallHierarchy",
      new PrepareCallHierarchyTool(client)
    )
  );

  context.subscriptions.push(
    vscode.lm.registerTool(
      "al_incomingCalls",
      new IncomingCallsTool(client)
    )
  );

  context.subscriptions.push(
    vscode.lm.registerTool(
      "al_outgoingCalls",
      new OutgoingCallsTool(client)
    )
  );

  context.subscriptions.push(
    vscode.lm.registerTool("al_codeLens", new CodeLensTool(client))
  );

  context.subscriptions.push(
    vscode.lm.registerTool(
      "al_codeQualityDiagnostics",
      new CodeQualityDiagnosticsTool(client)
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
