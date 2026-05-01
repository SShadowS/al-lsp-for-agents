import type { CellConfig } from "../lib/types.js";

/**
 * Two .app files in dependent-app/.alpackages both claim to be
 * "FBEMC - Main 1.0.0.0 by Dynamic Technology Partners, Inc." but with
 * DIFFERENT internal app ids. This is the most direct test of the
 * hypothesis behind AL0264/AL0197: when the LS sees the same extension
 * registered twice, it should fire "already declared by the extension X".
 *
 * If THIS doesn't reproduce the error, the bug is not a simple package
 * collision; it requires a more specific mechanism (e.g. shared cache
 * across LS instances).
 */
export const cellAllThreePkgCollision: CellConfig = {
  name: "cell-all-three-pkg-collision",
  description:
    "Two .app files claim same publisher/name/version (different ids) in .alpackages. Forces ambiguous extension registration.",
  extensionIds: [
    "ms-dynamics-smb.al",
    "local:al-lsp-for-agents",
    "jamespearson.al-test-runner",
  ],
  fixture: "pkg-collision",
  openSubFolder: "dependent-app",
  openFiles: [
    "dependent-app/src/Codeunits/Codeunit75200.al",
  ],
};
