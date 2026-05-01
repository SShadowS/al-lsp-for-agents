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
 * Default files to open when a cell does not declare openFiles.
 * Matches the layout of the test-al-project fixture.
 */
const DEFAULT_TARGET_FILES = [
  "src/Tables/Customer.Table.al",
];

describe(`cell: ${CELL_NAME}`, function () {
  this.timeout(180_000);

  it("captures diagnostics matching baseline", async function () {
    const cell = findCell(CELL_NAME);
    const TARGET_FILES = cell.openFiles ?? DEFAULT_TARGET_FILES;
    const log = (msg: string): void => {
      // stderr so test-electron forwards it to the parent console
      process.stderr.write(`[harness] ${CELL_NAME}: ${msg}\n`);
    };

    // Activate the Microsoft AL extension first so the AL language id is
    // registered before we open AL files (otherwise documents come up as
    // plaintext and the LS never engages).
    const alExt = vscode.extensions.getExtension("ms-dynamics-smb.al");
    log(`AL ext present=${!!alExt} active=${alExt?.isActive ?? false}`);
    if (alExt) {
      await alExt.activate();
      log(`AL ext activated, isActive=${alExt.isActive}`);
    }
    const workspaceFolders = vscode.workspace.workspaceFolders ?? [];
    log(`workspace folders=${workspaceFolders.map(f => f.uri.fsPath).join("|")}`);

    // Pre-open and SHOW each workspace folder's app.json (a non-AL file)
    // so vscode.window.activeTextEditor is set BEFORE AL Test Runner
    // gets activated by the first AL file open. AL Test Runner's
    // activate() calls getWorkspaceFolder() which throws "Please open a
    // file in the project you want to run the tests for" if there is
    // no active editor. This is critical for multi-root fixtures.
    for (const folder of workspaceFolders) {
      const appJsonUri = vscode.Uri.file(join(folder.uri.fsPath, "app.json"));
      try {
        const doc = await vscode.workspace.openTextDocument(appJsonUri);
        await vscode.window.showTextDocument(doc, { preview: false });
        log(`pre-opened ${folder.name}/app.json languageId=${doc.languageId}`);
      } catch (err) {
        log(`failed to pre-open ${folder.name}/app.json: ${(err as Error).message}`);
      }
    }

    // Open all target files. Use showTextDocument so AL Test Runner and
    // any other "needs an active editor" extensions see one when they
    // activate. The first opened file becomes active.
    const opened: vscode.TextDocument[] = [];
    for (const rel of TARGET_FILES) {
      const uri = vscode.Uri.file(join(FIXTURE, rel));
      const doc = await vscode.workspace.openTextDocument(uri);
      opened.push(doc);
      log(`opened ${rel} languageId=${doc.languageId} lines=${doc.lineCount}`);
    }
    if (opened.length > 0) {
      await vscode.window.showTextDocument(opened[0]!, { preview: false });
    }

    // Now that we have an active editor, activate the runner-and-wrapper
    // extensions that require workspace context.
    if (cell.extensionIds.includes("jamespearson.al-test-runner")) {
      const tr = vscode.extensions.getExtension("jamespearson.al-test-runner");
      log(`AL Test Runner present=${!!tr}`);
      if (tr) {
        await tr.activate();
        log(`AL Test Runner activated, isActive=${tr.isActive}`);
      }
    }
    if (cell.extensionIds.includes("local:al-lsp-for-agents")) {
      const w = vscode.extensions.getExtension("SShadowSdk.al-lsp-for-agents");
      log(`Wrapper present=${!!w}`);
      if (w) {
        await w.activate();
        log(`Wrapper activated, isActive=${w.isActive}`);
      }
    }

    // Subscribe to diagnostic changes and run a quiet-period tracker.
    // Bumped minElapsedMs to 45s — AL LS warmup with two AL apps in a
    // multi-root workspace easily takes 20-30s before the first
    // diagnostic batch lands. The previous 10s floor caused harness to
    // declare "settled" before AL LS had a chance to emit anything.
    const tracker = new QuietPeriodTracker({
      quietMs: 8_000,
      maxMs: 180_000,
      minElapsedMs: 45_000,
    });
    let activityCount = 0;
    const sub = vscode.languages.onDidChangeDiagnostics(() => {
      activityCount += 1;
      tracker.markActivity(Date.now());
    });
    // Mark initial "activity" so quietMs starts ticking immediately.
    tracker.markActivity(Date.now());

    try {
      await waitForSettled(tracker, 250);
    } finally {
      sub.dispose();
    }
    log(`settled. diagnostic-change events=${activityCount}`);

    // Collect raw diagnostics from EVERY URI VS Code knows about. The
    // bug we hunt (AL0264 / AL0197 — "already declared by extension X")
    // can fire against URIs that are not in our opened list (e.g. the
    // workspace folder URI, an .app symbol URI, or any other file the
    // LS analyzed). Restricting to opened URIs would have hidden the
    // very thing we want to capture.
    const raw: RawDiag[] = [];
    const allDiags = vscode.languages.getDiagnostics();
    for (const [uri, diags] of allDiags) {
      for (const diag of diags) {
        const codeAny = diag.code as
          | string
          | number
          | { value: string | number; target?: unknown }
          | undefined;
        const entry: RawDiag = {
          uri: uri.toString(),
          source: diag.source ?? "",
          severity: diag.severity,
          line: diag.range.start.line,
          character: diag.range.start.character,
        };
        if (codeAny !== undefined) entry.code = codeAny;
        raw.push(entry);
      }
    }

    log(`raw diagnostics count=${raw.length}`);
    for (const r of raw) {
      log(`  raw: ${r.uri} src=${r.source} code=${JSON.stringify(r.code)} sev=${r.severity}`);
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
