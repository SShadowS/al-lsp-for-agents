/**
 * Run a single matrix cell: launch VS Code via test-electron with the
 * cell's extensions installed, the per-cell fixture copy as the workspace,
 * and HARNESS_CELL/HARNESS_FIXTURE/HARNESS_OUT env vars wired through.
 *
 * Usage:
 *   tsx runOneCell.ts <cell-name> [--record]
 *
 * --record: write the captured snapshot to baseline/<cell>.json instead
 *           of comparing against it. Use to seed initial baselines.
 */

import { runTests } from "@vscode/test-electron";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { mkdir } from "node:fs/promises";
import { findCell } from "./cells/index.js";
import { makeFixtureCopy } from "./lib/fixture.js";
import { downloadVsix, loadLock, vsixCachePath } from "./scripts/fetch-vsix.js";

const HARNESS_DIR = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = dirname(HARNESS_DIR);
const LOCAL_EXTENSION = join(REPO_ROOT, "vscode-extension");

async function main(): Promise<void> {
  const cellName = process.argv[2];
  if (!cellName) {
    throw new Error("Usage: runOneCell.ts <cell-name> [--record]");
  }

  const suiteIndexPath = join(HARNESS_DIR, "out", "suite", "index.js");
  const { access } = await import("node:fs/promises");
  try {
    await access(suiteIndexPath);
  } catch {
    throw new Error(
      `Compiled suite not found at ${suiteIndexPath}. Run 'npm run build' first.`
    );
  }

  const record = process.argv.includes("--record");
  const cell = findCell(cellName);

  const lock = await loadLock();
  const installArgs: string[] = [];
  let extensionDevelopmentPath: string | undefined;

  for (const id of cell.extensionIds) {
    if (id === "local:al-lsp-for-agents") {
      extensionDevelopmentPath = LOCAL_EXTENSION;
      continue;
    }
    const pin = lock.extensions.find((p) => p.id === id);
    if (!pin) {
      throw new Error(
        `Cell ${cellName} references ${id} which is not in extensions.lock.json`
      );
    }
    await downloadVsix(pin);
    installArgs.push("--install-extension", vsixCachePath(pin));
  }

  const fixture = await makeFixtureCopy(cellName);
  const outDir = join(HARNESS_DIR, "out", "snapshots");
  await mkdir(outDir, { recursive: true });
  const outPath = join(outDir, `${cellName}.json`);

  const userDataDir = join(HARNESS_DIR, "out", "user-data", cellName);
  const extensionsDir = join(HARNESS_DIR, "out", "extensions", cellName);

  // extensionDevelopmentPath is set ONLY when the cell explicitly
  // requests the local wrapper (via "local:al-lsp-for-agents" in
  // its extensionIds). Setting it for cells that don't request the
  // wrapper would auto-activate the wrapper via its activationEvents,
  // contaminating the cell's diagnostic baseline.
  //
  // TestOptions requires extensionDevelopmentPath, so when the cell
  // doesn't request the wrapper, we use HARNESS_DIR (an inert extension
  // that is never loaded into launchArgs/extensionIds).
  const devPath = extensionDevelopmentPath ?? HARNESS_DIR;

  try {
    await runTests({
      version: lock.vscode,
      extensionDevelopmentPath: devPath,
      extensionTestsPath: join(HARNESS_DIR, "out", "suite", "index.js"),
      extensionTestsEnv: {
        HARNESS_CELL: cellName,
        HARNESS_FIXTURE: fixture.path,
        HARNESS_OUT: outPath,
        HARNESS_BASELINE_MODE: record ? "record" : "compare",
      },
      launchArgs: [
        fixture.path,
        "--user-data-dir",
        userDataDir,
        "--extensions-dir",
        extensionsDir,
        "--disable-workspace-trust",
        "--disable-telemetry",
        "--disable-updates",
        ...installArgs,
      ],
    });
    process.stderr.write(`cell ${cellName} OK; snapshot at ${outPath}\n`);
  } finally {
    await fixture.dispose();
  }
}

main().catch((err) => {
  console.error(err.message ?? err);
  process.exit(1);
});
