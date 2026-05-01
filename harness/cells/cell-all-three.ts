import type { CellConfig } from "../lib/types.js";

export const cellAllThree: CellConfig = {
  name: "cell-all-three",
  description:
    "Microsoft AL + AL LSP for Agents + AL Test Runner. Reproduces issue #17 with false 'already declared' errors.",
  extensionIds: [
    "ms-dynamics-smb.al",
    "local:al-lsp-for-agents",
    "jamespearson.al-test-runner",
  ],
};
