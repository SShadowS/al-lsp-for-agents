# Multi-Root Workspace Support Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Support multi-root workspaces so all AL project folders get proper LSP support, not just the first one.

**Architecture:** Single LanguageClient, single Go wrapper, single AL LS — matching the MS AL extension's "active workspace switching" model. The Go wrapper forwards all `workspaceFolders` to both the AL LS and al-call-hierarchy during initialization. The VS Code extension sends `al/setActiveWorkspace` before tool requests when the target file is in a different folder than the current active workspace. Workspace folder changes are forwarded via `al/didChangeWorkspaceFolders`.

**Tech Stack:** Go, TypeScript, VS Code Extension API, LSP

---

## Chunk 1: Go wrapper — forward workspace folders

### Task 1: Store and forward workspace folders in handleInitialize

**Files:**
- Modify: `al-language-server-go/wrapper/wrapper.go:50,607-635,936-946`

- [ ] **Step 1: Add `workspaceFolders` field to the wrapper struct**

In `wrapper.go`, after the `workspaceRoot` field (line 50), add:

```go
	workspaceRoot       string
	workspaceFolders    []WorkspaceFolder
```

- [ ] **Step 2: Extract and store workspace folders from initialize params**

In `handleInitialize`, after the `workspaceRoot` extraction block (after line 613), add:

```go
	// Extract workspace folders (multi-root support)
	if len(params.WorkspaceFolders) > 0 {
		w.workspaceFolders = params.WorkspaceFolders
		w.Log("Workspace folders (%d):", len(params.WorkspaceFolders))
		for _, folder := range params.WorkspaceFolders {
			w.Log("  - %s (%s)", folder.Name, folder.URI)
		}
	} else if w.workspaceRoot != "" {
		// Single-root fallback: construct one folder from rootUri
		w.workspaceFolders = []WorkspaceFolder{
			{URI: PathToFileURI(w.workspaceRoot), Name: filepath.Base(w.workspaceRoot)},
		}
		w.Log("Single workspace folder (from rootUri): %s", w.workspaceRoot)
	}
```

- [ ] **Step 3: Include workspace folders in AL LS initialize params**

In `handleInitialize`, after `initParams` is constructed (around line 635), add the workspace folders:

```go
	// Add all workspace folders to AL LS initialize params
	if len(w.workspaceFolders) > 0 {
		initParams.WorkspaceFolders = w.workspaceFolders
	}
```

- [ ] **Step 4: Pass all workspace folders to al-call-hierarchy**

In `startCallHierarchy` (around line 936-946), replace the single-folder construction:

Replace:
```go
	// Initialize with workspace root
	workspacePath := w.workspaceRoot
	if workspacePath == "" {
		workspacePath, _ = os.Getwd()
	}

	workspaceURI := PathToFileURI(workspacePath)
	workspaceName := filepath.Base(workspacePath)
	workspaceFolders := []WorkspaceFolder{
		{URI: workspaceURI, Name: workspaceName},
	}
```

With:
```go
	// Initialize with all workspace folders
	workspacePath := w.workspaceRoot
	if workspacePath == "" {
		workspacePath, _ = os.Getwd()
	}
	workspaceURI := PathToFileURI(workspacePath)

	// Use stored workspace folders (multi-root) or construct single folder
	workspaceFolders := w.workspaceFolders
	if len(workspaceFolders) == 0 {
		workspaceFolders = []WorkspaceFolder{
			{URI: workspaceURI, Name: filepath.Base(workspacePath)},
		}
	}

	w.Log("Initializing call hierarchy with %d workspace folders", len(workspaceFolders))
	for _, folder := range workspaceFolders {
		w.Log("  - %s (%s)", folder.Name, folder.URI)
	}
```

- [ ] **Step 5: Verify Go builds**

Run: `cd al-language-server-go && go build ./...`
Expected: No errors

- [ ] **Step 6: Commit**

```bash
git add al-language-server-go/wrapper/wrapper.go
git commit -m "feat: forward workspace folders to AL LS and al-call-hierarchy

Store workspaceFolders from client initialize params and pass them to
both the AL Language Server and al-call-hierarchy, instead of only
using rootUri. This enables multi-root workspace support."
```

### Task 2: Handle al/setActiveWorkspace forwarding

The Go wrapper needs to forward `al/setActiveWorkspace` requests from the VS Code extension to the AL LS without intercepting them, and track the active workspace for logging. The wrapper's default passthrough already forwards unknown methods, but we need to intercept it to track state.

**Files:**
- Modify: `al-language-server-go/wrapper/handlers.go`

- [ ] **Step 1: Add SetActiveWorkspaceHandler**

Add after the `WorkspaceSymbolHandler` (around line 729):

```go
// SetActiveWorkspaceHandler handles al/setActiveWorkspace
type SetActiveWorkspaceHandler struct{}

func (h *SetActiveWorkspaceHandler) ShouldHandle(method string) bool {
	return method == "al/setActiveWorkspace"
}

func (h *SetActiveWorkspaceHandler) Handle(msg *Message, w WrapperInterface) (*Message, *Message) {
	// Parse to extract workspace path for tracking
	var params struct {
		CurrentWorkspaceFolderPath struct {
			URI  string `json:"uri"`
			Name string `json:"name"`
		} `json:"currentWorkspaceFolderPath"`
	}
	if err := json.Unmarshal(msg.Params, &params); err == nil && params.CurrentWorkspaceFolderPath.URI != "" {
		w.Log("Switching active workspace to: %s (%s)",
			params.CurrentWorkspaceFolderPath.Name,
			params.CurrentWorkspaceFolderPath.URI)
	}

	// Forward to AL LSP
	var rawParams interface{}
	json.Unmarshal(msg.Params, &rawParams)

	response, err := w.SendRequestToLSP("al/setActiveWorkspace", rawParams)
	if err != nil {
		w.Log("al/setActiveWorkspace failed: %v", err)
		return nil, NewErrorResponse(msg.ID, InternalError, err.Error())
	}

	if response.Error != nil {
		return nil, &Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error:   response.Error,
		}
	}

	return &Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  response.Result,
	}, nil
}
```

- [ ] **Step 2: Add DidChangeWorkspaceFoldersHandler**

Add after the `SetActiveWorkspaceHandler`:

```go
// DidChangeWorkspaceFoldersHandler handles al/didChangeWorkspaceFolders (notification)
type DidChangeWorkspaceFoldersHandler struct{}

func (h *DidChangeWorkspaceFoldersHandler) ShouldHandle(method string) bool {
	return method == "al/didChangeWorkspaceFolders"
}

func (h *DidChangeWorkspaceFoldersHandler) Handle(msg *Message, w WrapperInterface) (*Message, *Message) {
	w.Log("Forwarding al/didChangeWorkspaceFolders to AL LSP and al-call-hierarchy")

	// Forward to AL LSP as notification (no response expected)
	w.SendNotificationToLSP("al/didChangeWorkspaceFolders", msg.Params)

	// Also forward to al-call-hierarchy using standard LSP notification
	// al-call-hierarchy supports workspace/didChangeWorkspaceFolders
	chServer := w.GetCallHierarchyServer()
	if chServer != nil && chServer.IsInitialized() {
		// Convert al/ format to standard LSP format for al-call-hierarchy
		chServer.SendNotification("workspace/didChangeWorkspaceFolders", msg.Params)
	}

	// Notification — no response to client
	return nil, nil
}
```

**Note:** This handler returns `nil, nil` because `al/didChangeWorkspaceFolders` is a **notification** (no ID, no response). The Go wrapper's `handleMessage` function already handles this correctly — when both return values are nil, no response is written to the client.

**Important:** This requires `SendNotificationToLSP` and `SendNotification` methods to exist on `WrapperInterface` and `CallHierarchyServer`. If they don't exist yet, they need to be added. `SendNotificationToLSP` writes a JSON-RPC message without an `id` field to the AL LS stdin. Check if the wrapper already has this capability — if not, add a simple method that writes `{"jsonrpc":"2.0","method":"...","params":...}` without an `id`.

- [ ] **Step 3: Register the handlers in GetDefaultHandlers**

In `GetDefaultHandlers()` (around line 907), add the two new handlers:

```go
func GetDefaultHandlers() []Handler {
	return []Handler{
		&DefinitionHandler{},
		&HoverHandler{},
		&DocumentSymbolHandler{},
		&WorkspaceSymbolHandler{},
		&ReferencesHandler{},
		NewCallHierarchyHandler(),
		&CodeLensHandler{},
		&SetActiveWorkspaceHandler{},
		&DidChangeWorkspaceFoldersHandler{},
	}
}
```

- [ ] **Step 4: Verify Go builds**

Run: `cd al-language-server-go && go build ./...`
Expected: No errors

- [ ] **Step 5: Rebuild both platform binaries**

Run:
```bash
cd al-language-server-go
go build -ldflags="-s -w" -o ../al-language-server-go-windows/bin/al-lsp-wrapper.exe .
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ../al-language-server-go-linux/bin/al-lsp-wrapper .
```

- [ ] **Step 6: Commit**

```bash
git add al-language-server-go/wrapper/handlers.go \
        al-language-server-go-windows/bin/al-lsp-wrapper.exe \
        al-language-server-go-linux/bin/al-lsp-wrapper
git commit -m "feat: add handlers for al/setActiveWorkspace and al/didChangeWorkspaceFolders

Forward these AL-specific workspace commands to the AL Language Server,
enabling the VS Code extension to switch active workspace folders at runtime."
```

## Chunk 2: VS Code extension — workspace switching

### Task 3: Track active workspace and switch before tool requests

**Files:**
- Modify: `vscode-extension/src/extension.ts`

- [ ] **Step 1: Add workspace tracking and switching logic**

Replace the entire contents of `extension.ts` with:

```typescript
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

// Reset on activation to prevent stale state after reactivation
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
    synchronize: {
      fileEvents: vscode.workspace.createFileSystemWatcher("**/*.al"),
    },
    // Suppress all VS Code provider registrations — the MS AL extension
    // already provides these for the editor. We only use client.sendRequest()
    // for our Language Model Tools, so we don't need duplicate providers.
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
  const log = vscode.window.createOutputChannel("AL LSP for Agents");
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
    vscode.workspace.onDidChangeWorkspaceFolders(async (event) => {
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
```

- [ ] **Step 2: Verify TypeScript compiles**

Run: `cd vscode-extension && npm run compile:tsc`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add vscode-extension/src/extension.ts
git commit -m "feat: add multi-root workspace support to VS Code extension

Track active workspace folder, send al/setActiveWorkspace when tool
requests target a different folder. Forward workspace folder add/remove
events via al/didChangeWorkspaceFolders."
```

### Task 4: Add ensureActiveWorkspace calls to tool classes

**Files:**
- Modify: `vscode-extension/src/tools.ts`

Every tool that takes a `uri` parameter needs to call `ensureActiveWorkspace(uri)` before sending its LSP request. This ensures the AL LS is focused on the correct project folder.

- [ ] **Step 1: Add the import at the top of tools.ts**

After the existing imports (line 2), add:

```typescript
import { ensureActiveWorkspace } from "./extension";
```

- [ ] **Step 2: Add ensureActiveWorkspace calls to URI-based tools**

For each tool class that receives a `uri` in `options.input`, add `await ensureActiveWorkspace(uri);` as the first line of the `invoke` method, after destructuring the input.

Tools to update (add `await ensureActiveWorkspace(uri);` after the input destructuring):

1. **GoToDefinitionTool** — after `const { uri, line, character } = options.input;`
2. **HoverTool** — after `const { uri, line, character } = options.input;`
3. **FindReferencesTool** — after `const { uri, line, character } = options.input;`
4. **PrepareCallHierarchyTool** — after `const { uri, line, character } = options.input;`
5. **IncomingCallsTool** — after `const { uri, line, character } = options.input;`
6. **OutgoingCallsTool** — after `const { uri, line, character } = options.input;`
7. **CodeLensTool** — after `const { uri } = options.input;`, add `if (uri) await ensureActiveWorkspace(uri);`
8. **DocumentSymbolsTool** — after the `if (!uri)` guard, add `await ensureActiveWorkspace(uri);`
9. **RenameSymbolTool** — after `const { uri, line, character, newName } = options.input;`

**Do NOT add to:** `CodeQualityDiagnosticsTool` (reads from VS Code diagnostics, not LSP).

Example for GoToDefinitionTool:

```typescript
class GoToDefinitionTool implements vscode.LanguageModelTool<PositionInput> {
  constructor(private client: LanguageClient) {}

  async invoke(
    options: vscode.LanguageModelToolInvocationOptions<PositionInput>,
    _token: vscode.CancellationToken
  ): Promise<vscode.LanguageModelToolResult> {
    const { uri, line, character } = options.input;
    await ensureActiveWorkspace(uri);
    // ... rest unchanged
```

- [ ] **Step 3: Verify TypeScript compiles**

Run: `cd vscode-extension && npm run compile:tsc`
Expected: No errors

- [ ] **Step 4: Build the extension**

Run: `cd vscode-extension && npm run compile`
Expected: No errors

- [ ] **Step 5: Commit**

```bash
git add vscode-extension/src/tools.ts
git commit -m "feat: switch active workspace before tool requests

Call ensureActiveWorkspace(uri) in each tool's invoke method so the
AL Language Server is focused on the correct project folder before
processing the request. Enables multi-root workspace support."
```
