import { strict as assert } from "node:assert";
import { readFile, writeFile, mkdir } from "node:fs/promises";
import { dirname, join } from "node:path";
import * as vscode from "vscode";
import {
  normalizeDiagnostics,
  diagnosticsEqual,
  type RawDiag,
  writeSnapshot,
} from "../lib/snapshot";
import { QuietPeriodTracker, waitForSettled } from "../lib/readiness";
import { findCell } from "../cells/index";
import type { DiagSnapshot, NormalizedDiag } from "../lib/types";

// __dirname is available in CJS (this file is compiled with module:CommonJS)
const HARNESS_DIR = dirname(dirname(__dirname));

/**
 * Driver-supplied environment:
 *   HARNESS_CELL — cell name being executed
 *   HARNESS_FIXTURE — absolute path to the per-cell fixture copy
 *   HARNESS_OUT — where to write the produced snapshot
 *   HARNESS_BASELINE_MODE — "compare" (default) or "record"
 */
function envOrThrow(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`Missing required env ${name}`);
  return v;
}

const CELL_NAME = envOrThrow("HARNESS_CELL");
const FIXTURE = envOrThrow("HARNESS_FIXTURE");
const OUT_PATH = envOrThrow("HARNESS_OUT");
const MODE = (process.env.HARNESS_BASELINE_MODE ?? "compare") as
  | "compare"
  | "record";

/**
 * Files in the fixture we want to open and capture diagnostics for.
 * Hard-coded list keeps results deterministic; if the fixture grows,
 * extend this list rather than globbing.
 */
const TARGET_FILES = [
  "src/Tables/Customer.Table.al",
];

describe(`cell: ${CELL_NAME}`, function () {
  this.timeout(180_000);

  it("captures diagnostics matching baseline", async function () {
    const cell = findCell(CELL_NAME);

    // Activate AL extension explicitly. Other extensions (al-test-runner,
    // wrapper) activate via their activation events when AL files open.
    const alExt = vscode.extensions.getExtension("ms-dynamics-smb.al");
    if (alExt) await alExt.activate();
    if (cell.extensionIds.includes("jamespearson.al-test-runner")) {
      const tr = vscode.extensions.getExtension("jamespearson.al-test-runner");
      if (tr) await tr.activate();
    }
    if (cell.extensionIds.includes("local:al-lsp-for-agents")) {
      const w = vscode.extensions.getExtension("SShadowSdk.al-lsp-for-agents");
      if (w) await w.activate();
    }

    // Open all target files. Use openTextDocument (not showTextDocument):
    // we don't need editor focus, just docs known to the language services.
    const opened: vscode.TextDocument[] = [];
    for (const rel of TARGET_FILES) {
      const uri = vscode.Uri.file(join(FIXTURE, rel));
      const doc = await vscode.workspace.openTextDocument(uri);
      opened.push(doc);
    }

    // Subscribe to diagnostic changes and run a quiet-period tracker.
    const tracker = new QuietPeriodTracker({
      quietMs: 5_000,
      maxMs: 120_000,
      minElapsedMs: 10_000,
    });
    const sub = vscode.languages.onDidChangeDiagnostics(() => {
      tracker.markActivity(Date.now());
    });
    // Mark initial "activity" so quietMs starts ticking immediately.
    tracker.markActivity(Date.now());

    try {
      await waitForSettled(tracker, 250);
    } finally {
      sub.dispose();
    }

    // Collect raw diagnostics for opened URIs only.
    const raw: RawDiag[] = [];
    for (const doc of opened) {
      for (const diag of vscode.languages.getDiagnostics(doc.uri)) {
        const codeAny = diag.code as
          | string
          | number
          | { value: string | number; target?: unknown }
          | undefined;
        const entry: RawDiag = {
          uri: doc.uri.toString(),
          source: diag.source ?? "",
          severity: diag.severity,
          line: diag.range.start.line,
          character: diag.range.start.character,
        };
        if (codeAny !== undefined) entry.code = codeAny;
        raw.push(entry);
      }
    }

    const normalized = normalizeDiagnostics(raw, FIXTURE);

    const snapshot: DiagSnapshot = {
      capturedAt: new Date().toISOString(),
      cell: CELL_NAME,
      diagnostics: normalized,
    };
    await writeSnapshot(OUT_PATH, snapshot);

    if (MODE === "record") {
      const baselinePath = join(HARNESS_DIR, "baseline", `${CELL_NAME}.json`);
      await mkdir(dirname(baselinePath), { recursive: true });
      await writeFile(
        baselinePath,
        JSON.stringify(
          { ...snapshot, capturedAt: "<recorded>" },
          null,
          2
        ) + "\n"
      );
      // Recording succeeds without comparison.
      return;
    }

    // Compare against baseline.
    const baselinePath = join(HARNESS_DIR, "baseline", `${CELL_NAME}.json`);
    const baselineRaw = await readFile(baselinePath, "utf8");
    const baseline = JSON.parse(baselineRaw) as DiagSnapshot;
    const expected: NormalizedDiag[] = baseline.diagnostics;

    if (!diagnosticsEqual(expected, normalized)) {
      const diff = describeDiff(expected, normalized);
      assert.fail(`Diagnostic snapshot mismatch for ${CELL_NAME}:\n${diff}`);
    }
  });
});

function describeDiff(
  expected: NormalizedDiag[],
  actual: NormalizedDiag[]
): string {
  const key = (d: NormalizedDiag) =>
    `${d.relUri}:${d.line}:${d.character}:${d.code}:${d.source}:${d.severity}`;
  const e = new Set(expected.map(key));
  const a = new Set(actual.map(key));
  const missing = [...e].filter((k) => !a.has(k));
  const extra = [...a].filter((k) => !e.has(k));
  const lines: string[] = [];
  for (const m of missing) lines.push(`  - missing: ${m}`);
  for (const x of extra) lines.push(`  + extra:   ${x}`);
  return lines.join("\n");
}
