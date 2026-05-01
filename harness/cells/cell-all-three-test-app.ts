import type { CellConfig } from "../lib/types.js";

/**
 * Multi-root with main-app source AND a test-app that depends on main.
 * Mimics what AL Test Runner produces: a separate test project that
 * compiles against the main app's .app symbol package. With both visible
 * to the same MS AL LS workspace, the LS may see "FBEMC - Main" twice
 * (source + symbol) and fire AL0264 / AL0197.
 */
export const cellAllThreeTestApp: CellConfig = {
  name: "cell-all-three-test-app",
  description:
    "Multi-root: FBEMC - Main as source + FBEMC - Tests depending on it via .app symbol. Closest to AL Test Runner setup.",
  extensionIds: [
    "ms-dynamics-smb.al",
    "local:al-lsp-for-agents",
    "jamespearson.al-test-runner",
  ],
  fixture: "with-test-app",
  workspaceFile: "with-test.code-workspace",
  openFiles: [
    "main-app/src/Codeunits/Codeunit75000.al",
    "test-app/src/Codeunits/Codeunit75500.al",
  ],
};
