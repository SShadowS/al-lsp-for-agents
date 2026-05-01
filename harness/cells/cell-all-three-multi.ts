import type { CellConfig } from "../lib/types.js";

/**
 * Multi-root workspace with two unrelated AL apps. Tests whether the MS
 * AL LS misbehaves on al/setActiveWorkspace switching between sibling
 * projects. The wrapper sends al/setActiveWorkspace whenever the active
 * project changes (see al-language-server-go/wrapper/wrapper.go:1195).
 *
 * If switching is racy, the LS may end up thinking one of the projects
 * is registered twice — feeding into the AL0264 mechanism.
 */
export const cellAllThreeMulti: CellConfig = {
  name: "cell-all-three-multi",
  description:
    "Multi-root workspace, two unrelated AL apps. Tests setActiveWorkspace switching.",
  extensionIds: [
    "ms-dynamics-smb.al",
    "local:al-lsp-for-agents",
    "jamespearson.al-test-runner",
  ],
  fixture: "multi-app-workspace",
  workspaceFile: "multi.code-workspace",
  openFiles: [
    "app-a/src/Codeunits/Codeunit80000.al",
    "app-b/src/Codeunits/Codeunit80200.al",
  ],
};
