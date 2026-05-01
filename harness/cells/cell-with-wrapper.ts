import type { CellConfig } from "../lib/types.js";

export const cellWithWrapper: CellConfig = {
  name: "cell-with-wrapper",
  description:
    "Microsoft AL + AL LSP for Agents (locally built). Wrapper alone should be clean.",
  extensionIds: ["ms-dynamics-smb.al", "local:al-lsp-for-agents"],
};
