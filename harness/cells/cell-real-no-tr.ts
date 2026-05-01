import type { CellConfig } from "../lib/types.js";

/**
 * Control: same real fixture as cell-real-do-support but WITHOUT AL
 * Test Runner. Tests whether the bug requires both extensions or just
 * the wrapper. DigiTecKid's bisection said both wrapper AND TR were
 * required; this verifies that with the real fixture.
 */
export const cellRealNoTr: CellConfig = {
  name: "cell-real-no-tr",
  description:
    "Real DO.Support-UISimply project, AL + wrapper only (no Test Runner). Causal control for issue #17.",
  extensionIds: [
    "ms-dynamics-smb.al",
    "local:al-lsp-for-agents",
  ],
  fixture: "../DO.Support-UISimply/Core",
  workspaceFile: "CoreBase.code-workspace",
  openFiles: [
    "Cloud/Al/Activation/AppExperience.Codeunit.al",
  ],
};
