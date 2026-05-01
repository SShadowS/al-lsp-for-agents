import type { CellConfig } from "../lib/types.js";

export const cellWithTestRunner: CellConfig = {
  name: "cell-with-test-runner",
  description:
    "Microsoft AL + AL Test Runner. AL Test Runner alone should be clean.",
  extensionIds: ["ms-dynamics-smb.al", "jamespearson.al-test-runner"],
};
