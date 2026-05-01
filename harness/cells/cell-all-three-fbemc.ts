import type { CellConfig } from "../lib/types.js";

/**
 * Same extension set as cell-all-three but uses the fbemc-shape fixture:
 * a multi-root workspace with FBEMC - Main (source project) AND its
 * compiled .app symbol package present via the dependent-app's
 * .alpackages. This mirrors DigiTecKid's project shape from issue #17.
 *
 * Hypothesis: the duplicate-extension AL0264/AL0197 errors fire only when
 * the LS sees an extension as both source AND symbol package, AND the
 * wrapper's inner LS shares cache state with the primary.
 */
export const cellAllThreeFbemc: CellConfig = {
  name: "cell-all-three-fbemc",
  description:
    "Three extensions on fbemc-shape (multi-root, FBEMC - Main as source + .app). Issue #17 repro candidate #1.",
  extensionIds: [
    "ms-dynamics-smb.al",
    "local:al-lsp-for-agents",
    "jamespearson.al-test-runner",
  ],
  fixture: "fbemc-shape",
  // Open multi-root: both main-app (source FBEMC - Main) and dependent-app
  // (its .alpackages contains FBEMC - Main .app). Two LSP-visible copies
  // of the same extension; if MS LS double-registers, AL0264/AL0197 fires.
  workspaceFile: "fbemc.code-workspace",
  openFiles: [
    "main-app/src/Codeunits/Codeunit75000.GeneralEventSubscriptions.al",
    "dependent-app/src/Codeunits/Codeunit75200.UseFbemc.al",
  ],
};
