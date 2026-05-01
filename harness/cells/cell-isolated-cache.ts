import type { CellConfig } from "../lib/types.js";

/**
 * Same extension set as cell-all-three but the wrapper is told to use a
 * separate AL extension cache directory via AL_LSP_ALT_EXT_DIR. If issue
 * #17 disappears in this cell, that's strong causal evidence the bug is
 * shared-cache-driven, independent of whether Layer 1 ships.
 *
 * The wrapper currently does not honor AL_LSP_ALT_EXT_DIR. Adding that
 * support is part of the implementation prerequisites for this cell:
 * see Task 14.
 */
import { tmpdir } from "node:os";
import { join } from "node:path";

export const cellIsolatedCache: CellConfig = {
  name: "cell-isolated-cache",
  description:
    "All three with wrapper using a separate AL extension cache. Causal probe for shared-cache hypothesis.",
  extensionIds: [
    "ms-dynamics-smb.al",
    "local:al-lsp-for-agents",
    "jamespearson.al-test-runner",
  ],
  wrapperEnv: {
    AL_LSP_ALT_EXT_DIR: join(tmpdir(), "al-lsp-isolated-cache"),
  },
};
