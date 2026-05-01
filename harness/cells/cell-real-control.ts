import type { CellConfig } from "../lib/types.js";

/**
 * Control: real fixture, ONLY Microsoft AL extension. If AL0264/AL0197
 * fires here, the bug is not caused by either the wrapper or AL Test
 * Runner — it's intrinsic to this real project + AL extension.
 */
export const cellRealControl: CellConfig = {
  name: "cell-real-control",
  description:
    "Real DO.Support-UISimply project, Microsoft AL only. Baseline for what the LS produces by itself.",
  extensionIds: [
    "ms-dynamics-smb.al",
  ],
  fixture: "../DO.Support-UISimply/Core",
  workspaceFile: "CoreBase.code-workspace",
  openFiles: [
    "Cloud/Al/Activation/AppExperience.Codeunit.al",
  ],
};
