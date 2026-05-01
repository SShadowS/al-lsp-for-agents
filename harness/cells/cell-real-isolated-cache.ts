import { tmpdir } from "node:os";
import { join } from "node:path";
import type { CellConfig } from "../lib/types.js";

/**
 * Same as cell-real-do-support but the wrapper is told to use a
 * separate AL extension cache directory via AL_LSP_ALT_EXT_DIR. Option A
 * from the plan: if the bug disappears here, the shared-cache hypothesis
 * is confirmed and the fix is to ship Layer 1 around isolated caches.
 */
export const cellRealIsolatedCache: CellConfig = {
  name: "cell-real-isolated-cache",
  description:
    "Real DO.Support-UISimply project, all three extensions, wrapper using AL_LSP_ALT_EXT_DIR. Tests Option A.",
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
  wrapperEnv: {
    AL_LSP_ALT_EXT_DIR: join(tmpdir(), "al-lsp-isolated-cache-real"),
  },
};
