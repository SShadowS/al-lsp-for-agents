import type { CellConfig } from "../lib/types.js";

/**
 * Bisection scratchpad cell. Mutable from the environment so we can
 * iterate quickly while shrinking a real reproducer down to a minimal
 * one without writing a new cell file per attempt.
 *
 * Configure via env vars:
 *   HARNESS_BISECT_FIXTURE   — fixture path (absolute or repo-relative)
 *   HARNESS_BISECT_WORKSPACE — optional .code-workspace inside fixture
 *   HARNESS_BISECT_SUBFOLDER — optional sub-folder of fixture to open
 *   HARNESS_BISECT_OPEN      — comma-separated list of files to open
 *   HARNESS_BISECT_EXTS      — "control" (MS AL only) | "all" (all 3)
 *
 * Defaults to the same setup as cell-real-control on
 * harness/work/Core/ — i.e. our scratch workdir for shrinking
 * DO.Support-UISimply.
 */
const fixture = process.env.HARNESS_BISECT_FIXTURE ?? "harness/work/Core";
const workspaceFile = process.env.HARNESS_BISECT_WORKSPACE;
const openSubFolder = process.env.HARNESS_BISECT_SUBFOLDER;
const openCsv =
  process.env.HARNESS_BISECT_OPEN ??
  "Cloud/Al/Activation/AppExperience.Codeunit.al";
const exts = process.env.HARNESS_BISECT_EXTS ?? "control";

const extensionIds =
  exts === "all"
    ? [
        "ms-dynamics-smb.al",
        "local:al-lsp-for-agents",
        "jamespearson.al-test-runner",
      ]
    : ["ms-dynamics-smb.al"];

export const cellBisect: CellConfig = {
  name: "cell-bisect",
  description:
    "Mutable cell driven by HARNESS_BISECT_* env vars for shrinking real reproducers to minimal ones.",
  extensionIds,
  fixture,
  ...(workspaceFile ? { workspaceFile } : {}),
  ...(openSubFolder ? { openSubFolder } : {}),
  openFiles: openCsv.split(",").map((s) => s.trim()).filter(Boolean),
};
