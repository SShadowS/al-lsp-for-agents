import type { CellConfig } from "../lib/types.js";

/**
 * Control: same real fixture as cell-real-do-support but WITHOUT the
 * AL LSP for Agents wrapper. Tests whether the AL0264/AL0197 reproduces
 * with only Microsoft AL + AL Test Runner, or whether the wrapper is
 * required to trigger it.
 *
 * If this cell ALSO produces AL0264/AL0197, the bug is upstream and
 * unrelated to our wrapper — DigiTecKid's bisection finding "disable AL
 * LSP for Agents → bug goes away" would be wrong, or the conditions
 * differ from his.
 *
 * If this cell does NOT produce them but cell-real-do-support does, the
 * wrapper is causally implicated.
 */
export const cellRealNoWrapper: CellConfig = {
  name: "cell-real-no-wrapper",
  description:
    "Real DO.Support-UISimply project, AL + AL Test Runner only (no wrapper). Causal control for issue #17.",
  extensionIds: [
    "ms-dynamics-smb.al",
    "jamespearson.al-test-runner",
  ],
  fixture: "../DO.Support-UISimply/Core",
  workspaceFile: "CoreBase.code-workspace",
  openFiles: [
    "Cloud/Al/Activation/AppExperience.Codeunit.al",
  ],
};
