export interface ExtensionPin {
  /** Marketplace extension id, e.g. "ms-dynamics-smb.al" */
  id: string;
  /** Pinned version, e.g. "18.0.2190758" */
  version: string;
  /**
   * Hex-encoded sha256 of the downloaded .vsix.
   * Empty string until populated by `npm run refresh`.
   */
  sha256: string;
}

export interface ExtensionLockFile {
  /** Lock file schema version. Bumped on breaking changes. */
  schema: 1;
  /** Pinned VS Code version used by test-electron. */
  vscode: string;
  /** Extensions, keyed by id, in stable order. */
  extensions: ExtensionPin[];
}

export interface CellConfig {
  /** Filename-safe name, e.g. "cell-control" */
  name: string;
  /** Human-readable description. */
  description: string;
  /** Extension ids (must exist in lock file) installed for this cell. */
  extensionIds: string[];
  /**
   * Optional environment variables passed to the wrapper if al-lsp-for-agents
   * is in extensionIds. Used by the isolated-cache experiment cell.
   */
  wrapperEnv?: Record<string, string>;
  /**
   * Optional fixture name. Maps to harness/fixtures/<name>/. When omitted
   * the default test-al-project at the repo root is used. Variant fixtures
   * exist to reproduce multi-app scenarios that the default fixture is too
   * small to trigger (issue #17).
   */
  fixture?: string;
  /**
   * Optional list of files inside the fixture to open instead of the default
   * Customer.Table.al. When the fixture is multi-app these should point
   * into the dependent app so loading triggers symbol-package resolution
   * across both apps. Paths are forward-slash, relative to fixture root.
   */
  openFiles?: string[];
  /**
   * Optional path inside the fixture to a .code-workspace file. When set,
   * VS Code opens the workspace file instead of the fixture root as a
   * single folder. Used by multi-root fixtures (issue #17 repros).
   */
  workspaceFile?: string;
  /**
   * Optional sub-folder of the fixture to open as the VS Code workspace
   * root instead of fixture root. Mutually exclusive with workspaceFile.
   * Used by fixtures that ship multiple AL apps but want one opened
   * single-root.
   */
  openSubFolder?: string;
}

export interface NormalizedDiag {
  /** File URI relative to fixture root, with forward slashes. */
  relUri: string;
  /** Diagnostic source, e.g. "al" or "al-call-hierarchy". */
  source: string;
  /**
   * Diagnostic code as string (handles string|number|object code shapes).
   * Empty string when the LSP server provided no code.
   */
  code: string;
  /** "Error" | "Warning" | "Information" | "Hint" */
  severity: string;
  /** 0-based line of range start. */
  line: number;
  /** 0-based character of range start. */
  character: number;
}

export interface DiagSnapshot {
  /** ISO timestamp the snapshot was captured (informational, not compared). */
  capturedAt: string;
  /** Cell name this snapshot was captured for. */
  cell: string;
  /** Normalized diagnostics, sorted stably. Compared field-for-field. */
  diagnostics: NormalizedDiag[];
}
