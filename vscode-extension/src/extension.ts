import * as path from "path";
import * as vscode from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
} from "vscode-languageclient/node";
import { registerTools } from "./tools";

let client: LanguageClient;
let lastActiveWorkspacePath: string | undefined;
let log: vscode.OutputChannel;

function resetState() {
  lastActiveWorkspacePath = undefined;
}

/**
 * Send al/setActiveWorkspace to switch the AL LS focus to the folder
 * containing the given file URI. No-op if already active.
 */
export async function ensureActiveWorkspace(
  fileUri: string
): Promise<void> {
  if (!client?.isRunning()) return;

  const uri = vscode.Uri.parse(fileUri);
  const folder = vscode.workspace.getWorkspaceFolder(uri);
  if (!folder) return;

  const folderPath = folder.uri.fsPath;
  if (lastActiveWorkspacePath === folderPath) return;

  const alConfig = vscode.workspace.getConfiguration("al", folder.uri);

  try {
    const result = await client.sendRequest<{ success: boolean }>(
      "al/setActiveWorkspace",
      {
        currentWorkspaceFolderPath: {
          uri: folder.uri.toString(),
          name: folder.name,
          index: folder.index,
        },
        settings: {
          workspacePath: folderPath,
          alResourceConfigurationSettings: {
            packageCachePaths: alConfig.get("packageCachePath") ?? [],
            enableCodeAnalysis: alConfig.get("enableCodeAnalysis") ?? false,
            codeAnalyzers: alConfig.get("codeAnalyzers") ?? [],
            backgroundCodeAnalysis: alConfig.get("backgroundCodeAnalysis") ?? "full",
            assemblyProbingPaths: alConfig.get("assemblyProbingPaths") ?? [],
          },
          setActiveWorkspace: true,
          expectedProjectReferenceDefinitions: [],
          activeWorkspaceClosure: [],
        },
      }
    );

    if (result?.success) {
      lastActiveWorkspacePath = folderPath;
    }
  } catch {
    // Workspace switch failed — continue with current workspace
  }
}

export async function activate(context: vscode.ExtensionContext) {
  resetState();

  // Find the AL extension path
  const alExtension = vscode.extensions.getExtension("ms-dynamics-smb.al");
  const alExtensionPath = alExtension?.extensionPath ?? "";

  if (!alExtensionPath) {
    vscode.window.showWarningMessage(
      "AL LSP for Agents: MS AL extension not found. Some features may not work."
    );
  }

  // Determine wrapper binary path
  const binName =
    process.platform === "win32" ? "al-lsp-wrapper.exe" : "al-lsp-wrapper";
  const serverPath = path.join(context.extensionPath, "bin", binName);

  const args: string[] = [];
  if (alExtensionPath) {
    args.push("--al-extension-path", alExtensionPath);
  }

  const serverOptions: ServerOptions = {
    command: serverPath,
    args,
    options: { env: { ...process.env } },
  };

  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ scheme: "file", language: "al" }],
    // Suppress all VS Code provider registrations — the MS AL extension
    // already provides these for the editor. We only use client.sendRequest()
    // for our Language Model Tools, so we don't need duplicate providers.
    // Document sync (didOpen etc.) is left enabled — the LanguageClient
    // requires it to keep the server alive. BackgroundCodeAnalysis="None"
    // in the wrapper prevents heavy compilation from these events.
    middleware: {
      provideCompletionItem: () => undefined,
      provideHover: () => undefined,
      provideSignatureHelp: () => undefined,
      provideDefinition: () => undefined,
      provideReferences: () => undefined,
      provideDocumentHighlights: () => undefined,
      provideDocumentSymbols: () => undefined,
      provideWorkspaceSymbols: () => undefined,
      provideCodeActions: () => undefined,
      provideCodeLenses: () => undefined,
      resolveCodeLens: () => undefined,
      provideDocumentFormattingEdits: () => undefined,
      provideDocumentRangeFormattingEdits: () => undefined,
      provideOnTypeFormattingEdits: () => undefined,
      provideRenameEdits: () => undefined,
      provideDocumentLinks: () => undefined,
      provideFoldingRanges: () => undefined,
      provideSelectionRanges: () => undefined,
      provideDocumentSemanticTokens: () => undefined,
      provideDocumentRangeSemanticTokens: () => undefined,
      provideImplementation: () => undefined,
      provideTypeDefinition: () => undefined,
      provideDeclaration: () => undefined,
      provideInlayHints: () => undefined,
      provideInlineValues: () => undefined,
      prepareCallHierarchy: () => undefined,
      provideCallHierarchyIncomingCalls: () => undefined,
      provideCallHierarchyOutgoingCalls: () => undefined,
      handleDiagnostics: (_uri, diagnostics, next) => {
        // Only show al-call-hierarchy diagnostics (code quality).
        // The MS AL extension handles all compiler diagnostics — showing
        // compiler diagnostics from our second AL LSP instance would cause
        // duplicates or false "already declared" errors (issue #15).
        const enabled = vscode.workspace
          .getConfiguration("alLspForAgents")
          .get<boolean>("enableCodeQualityDiagnostics", false);

        // Log what we receive and filter for debugging (issue #15)
        const compiler = diagnostics.filter(
          (d) => d.source !== "al-call-hierarchy"
        );
        const codeQuality = diagnostics.filter(
          (d) => d.source === "al-call-hierarchy"
        );
        if (compiler.length > 0 || codeQuality.length > 0) {
          const file = _uri.path.split("/").pop();
          log?.appendLine(
            `[diag] ${file}: ${compiler.length} compiler (blocked), ${codeQuality.length} code-quality (${enabled ? "shown" : "hidden"})`
          );
        }

        if (enabled) {
          next(
            _uri,
            diagnostics.filter((d) => d.source === "al-call-hierarchy")
          );
        } else {
          next(_uri, []);
        }
      },
    },
  };

  client = new LanguageClient(
    "alLspForAgents",
    "AL LSP for Agents",
    serverOptions,
    clientOptions
  );

  // Register Language Model Tools for Copilot agent mode first —
  // tools should be available even if the LSP server is still starting
  log = vscode.window.createOutputChannel("AL LSP for Agents");
  log.appendLine("Extension activating...");
  log.appendLine(`vscode.lm available: ${!!vscode.lm}`);
  log.appendLine(`vscode.lm.registerTool available: ${!!vscode.lm?.registerTool}`);
  try {
    registerTools(context, client);
    log.appendLine("Tools registered successfully");
  } catch (err) {
    log.appendLine(`Tool registration failed: ${err}`);
    vscode.window.showErrorMessage(`AL LSP for Agents: Tool registration failed: ${err}`);
  }

  // Forward workspace folder changes to the AL LS
  context.subscriptions.push(
    vscode.workspace.onDidChangeWorkspaceFolders((event) => {
      if (!client?.isRunning()) return;
      try {
        client.sendNotification("al/didChangeWorkspaceFolders", {
          added: event.added.map((f) => ({ name: f.name, uri: f.uri.toString() })),
          removed: event.removed.map((f) => ({ name: f.name, uri: f.uri.toString() })),
        });
      } catch {
        // Non-critical — workspace folder sync failed
      }
    })
  );

  try {
    await client.start();
  } catch (err) {
    vscode.window.showErrorMessage(
      `AL LSP for Agents: Failed to start language server: ${err}`
    );
  }
}

export function deactivate(): Thenable<void> | undefined {
  return client?.stop();
}
