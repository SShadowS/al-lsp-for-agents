import type { CellConfig } from "../lib/types.js";

/**
 * REAL multi-app BC project from u:\Git\DO.Support-UISimply\Core. This
 * is the "FBEMC - Main + tests" shape from issue #17 but with a real
 * Continia codebase: Cloud (Continia Core source) and Test (test suite
 * depending on Continia Core via .app symbol).
 *
 * Synthetic fixtures (fbemc-shape, pkg-collision, multi-app, with-test-app)
 * all failed to reproduce AL0264/AL0197. This cell points at a real
 * project to see whether the bug fires there. If yes, we minimize from
 * the real fixture toward a synthetic one. If no, the bug needs even
 * more specific conditions (the actual FBEMC repo state).
 */
export const cellRealDoSupport: CellConfig = {
  name: "cell-real-do-support",
  description:
    "Cell-all-three on real BC project u:/Git/DO.Support-UISimply/Core (multi-root via CoreBase.code-workspace).",
  extensionIds: [
    "ms-dynamics-smb.al",
    "local:al-lsp-for-agents",
    "jamespearson.al-test-runner",
  ],
  fixture: "../DO.Support-UISimply/Core",
  workspaceFile: "CoreBase.code-workspace",
  openFiles: [
    "Cloud/Al/Activation/AppExperience.Codeunit.al",
  ],
};
