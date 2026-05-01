/**
 * Iterate over all cells, running each via runOneCell.ts as a subprocess
 * (for full process isolation between cells). Aggregate pass/fail.
 *
 * Usage:
 *   tsx runMatrix.ts [--record] [--only <cell-name>]
 */

import { spawn } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { ALL_CELLS } from "./cells/index.js";

const HARNESS_DIR = dirname(fileURLToPath(import.meta.url));

interface Result {
  cell: string;
  status: "pass" | "fail";
  durationMs: number;
  output: string;
}

async function runCell(cellName: string, args: string[]): Promise<Result> {
  const start = Date.now();
  const child = spawn(
    "npx",
    ["tsx", join(HARNESS_DIR, "runOneCell.ts"), cellName, ...args],
    {
      cwd: HARNESS_DIR,
      stdio: ["ignore", "pipe", "pipe"],
      shell: process.platform === "win32",
    }
  );
  const chunks: string[] = [];
  child.stdout?.on("data", (b: Buffer) => chunks.push(b.toString("utf8")));
  child.stderr?.on("data", (b: Buffer) => chunks.push(b.toString("utf8")));
  const code: number = await new Promise((resolve) => {
    child.on("exit", (c) => resolve(c ?? 1));
  });
  return {
    cell: cellName,
    status: code === 0 ? "pass" : "fail",
    durationMs: Date.now() - start,
    output: chunks.join(""),
  };
}

async function main(): Promise<void> {
  const args = process.argv.slice(2);
  const record = args.includes("--record");
  const onlyIdx = args.indexOf("--only");
  const only = onlyIdx >= 0 ? args[onlyIdx + 1] : undefined;

  const cells = only
    ? ALL_CELLS.filter((c) => c.name === only)
    : ALL_CELLS;
  if (cells.length === 0) {
    throw new Error(`No cells matched ${only ?? "<all>"}`);
  }

  const results: Result[] = [];
  for (const cell of cells) {
    process.stderr.write(`\n=== ${cell.name} ===\n`);
    const r = await runCell(cell.name, record ? ["--record"] : []);
    results.push(r);
    process.stderr.write(r.output);
    process.stderr.write(`-> ${r.status} (${r.durationMs}ms)\n`);
  }

  const failed = results.filter((r) => r.status === "fail");
  process.stderr.write("\n=== Summary ===\n");
  for (const r of results) {
    process.stderr.write(`  ${r.status.padEnd(4)}  ${r.cell}\n`);
  }
  process.exit(failed.length > 0 ? 1 : 0);
}

main().catch((err) => {
  console.error(err.message ?? err);
  process.exit(1);
});
