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

    const hoverContent =
      typeof hover.contents === "string"
        ? hover.contents
        : hover.contents.value;

    // Try to enrich with full XML doc comments from the source
    const xmlDoc = await this.getXmlDoc(uri, line, character);
    const result = xmlDoc
      ? `${hoverContent}\n\n--- Documentation ---\n${xmlDoc}`
      : hoverContent;

    return new vscode.LanguageModelToolResult([
      new vscode.LanguageModelTextPart(result),
    ]);
  }

  private async getXmlDoc(
    uri: string,
    line: number,
    character: number
  ): Promise<string | null> {
    try {
      // Go to definition to find the source location
      const locations = await this.client.sendRequest<
        { uri: string; range: Range }[] | { uri: string; range: Range } | null
      >("textDocument/definition", {
        textDocument: { uri },
        position: { line, character },
      });

      if (!locations) return null;

      const loc = Array.isArray(locations) ? locations[0] : locations;
      if (!loc) return null;

      // Read the source file
      const docUri = vscode.Uri.parse(loc.uri);
      let doc: vscode.TextDocument;
      try {
        doc = await vscode.workspace.openTextDocument(docUri);
      } catch {
        return null;
      }

      // Walk backwards from the definition line collecting /// lines
      const defLine = loc.range.start.line;
      const commentLines: string[] = [];
      for (let i = defLine - 1; i >= 0; i--) {
        const text = doc.lineAt(i).text.trim();
        if (text.startsWith("///")) {
          commentLines.unshift(text.replace(/^\/\/\/\s?/, ""));
        } else {
          break;
        }
      }

      if (commentLines.length === 0) return null;

      return parseXmlDoc(commentLines.join("\n"));
    } catch {
      return null;
    }
  }
}

function parseXmlDoc(xml: string): string {
  const parts: string[] = [];

  // Extract <summary>
  const summary = extractTag(xml, "summary");
  if (summary) {
    parts.push(summary.trim());
  }

  // Extract <param> tags
  const paramRegex = /<param\s+name=["']([^"']+)["']>([\s\S]*?)<\/param>/g;
  let match;
  const params: string[] = [];
  while ((match = paramRegex.exec(xml)) !== null) {
    params.push(`- \`${match[1]}\`: ${match[2].trim()}`);
  }
  if (params.length > 0) {
    parts.push(`**Parameters:**\n${params.join("\n")}`);
  }

  // Extract <returns>
  const returns = extractTag(xml, "returns");
  if (returns) {
    parts.push(`**Returns:** ${returns.trim()}`);
  }

  // Extract <remarks>
  const remarks = extractTag(xml, "remarks");
  if (remarks) {
    parts.push(`**Remarks:** ${remarks.trim()}`);
  }

  // Extract <example>
  const example = extractTag(xml, "example");
  if (example) {
    parts.push(`**Example:** ${example.trim()}`);
  }

  return parts.join("\n\n");
}

function extractTag(xml: string, tag: string): string | null {
  const regex = new RegExp(`<${tag}>([\\s\\S]*?)<\\/${tag}>`, "i");
  const match = regex.exec(xml);
  return match ? match[1] : null;
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
