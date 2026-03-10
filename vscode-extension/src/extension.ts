import * as path from "path";
import * as vscode from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
} from "vscode-languageclient/node";
import { registerTools } from "./tools";

let client: LanguageClient;

export async function activate(context: vscode.ExtensionContext) {
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
    synchronize: {
      fileEvents: vscode.workspace.createFileSystemWatcher("**/*.al"),
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
  registerTools(context, client);

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
