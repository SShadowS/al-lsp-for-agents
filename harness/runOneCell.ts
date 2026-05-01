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

import {
  runTests,
  downloadAndUnzipVSCode,
  resolveCliArgsFromVSCodeExecutablePath,
} from "@vscode/test-electron";
import { spawn } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { mkdir, readdir, copyFile, rename, stat } from "node:fs/promises";
import { findCell } from "./cells/index.js";
import { makeFixtureCopy } from "./lib/fixture.js";
import { downloadVsix, loadLock, vsixCachePath } from "./scripts/fetch-vsix.js";

const HARNESS_DIR = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = dirname(HARNESS_DIR);
const LOCAL_EXTENSION = join(REPO_ROOT, "vscode-extension");
const TRACE_PROXY_EXE = join(
  REPO_ROOT,
  "al-language-server-go",
  "cmd",
  "lsp-trace-proxy",
  "lsp-trace-proxy.exe"
);

async function exists(p: string): Promise<boolean> {
  try {
    await stat(p);
    return true;
  } catch {
    return false;
  }
}

/**
 * If HARNESS_BISECT_TRACE=1, swap the AL extension's
 * Microsoft.Dynamics.Nav.EditorServices.Host.exe with our stdio trace
 * proxy. The renamed real apphost (.exe.real) still resolves to
 * Host.dll because the .NET apphost stub embeds the dll path at
 * publish time. Returns the absolute path to the trace NDJSON file
 * for the env var, or undefined if tracing is disabled.
 *
 * Idempotent: tolerates an existing swap from a prior run.
 */
async function maybeInstallTraceProxy(
  extensionsDir: string,
  cellName: string
): Promise<string | undefined> {
  if (process.env.HARNESS_BISECT_TRACE !== "1") return undefined;

  const entries = await readdir(extensionsDir);
  const alDir = entries.find((e) => e.startsWith("ms-dynamics-smb.al-"));
  if (!alDir) {
    throw new Error(
      `HARNESS_BISECT_TRACE=1 but no ms-dynamics-smb.al-* in ${extensionsDir}`
    );
  }
  const win32 = join(extensionsDir, alDir, "bin", "win32");
  const realName = "Microsoft.Dynamics.Nav.EditorServices.Host.exe";
  const realPath = join(win32, realName);
  const backupPath = realPath + ".real";

  if (!(await exists(TRACE_PROXY_EXE))) {
    throw new Error(
      `lsp-trace-proxy.exe not built at ${TRACE_PROXY_EXE}. Run 'go build ./cmd/lsp-trace-proxy/' in al-language-server-go first.`
    );
  }

  if (!(await exists(backupPath))) {
    if (!(await exists(realPath))) {
      throw new Error(`expected ${realPath} after install, none found`);
    }
    await rename(realPath, backupPath);
  }
  await copyFile(TRACE_PROXY_EXE, realPath);

  const tracesDir = join(HARNESS_DIR, "out", "traces");
  await mkdir(tracesDir, { recursive: true });
  return join(tracesDir, `${cellName}.ndjson`);
}

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
  const vsixToInstall: string[] = [];
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
    vsixToInstall.push(vsixCachePath(pin));
  }

  const fixture = await makeFixtureCopy(cellName, cell.fixture);
  const outDir = join(HARNESS_DIR, "out", "snapshots");
  await mkdir(outDir, { recursive: true });
  const outPath = join(outDir, `${cellName}.json`);

  const userDataDir = join(HARNESS_DIR, "out", "user-data", cellName);
  const extensionsDir = join(HARNESS_DIR, "out", "extensions", cellName);

  if (cell.workspaceFile && cell.openSubFolder) {
    throw new Error(
      `Cell ${cellName}: workspaceFile and openSubFolder are mutually exclusive`
    );
  }
  let openTarget = fixture.path;
  if (cell.workspaceFile) {
    openTarget = join(fixture.path, cell.workspaceFile);
  } else if (cell.openSubFolder) {
    openTarget = join(fixture.path, cell.openSubFolder);
  }

  // Pre-install marketplace extensions into the per-cell extensions dir.
  // launchArgs --install-extension is a no-op when extensionTestsPath is
  // also set: VS Code skips the extension-management CLI path and goes
  // straight to test-host mode. We must invoke `code --install-extension`
  // ourselves in a separate step before runTests().
  if (vsixToInstall.length > 0) {
    const vscodeExe = await downloadAndUnzipVSCode(lock.vscode);
    const cliArgs = resolveCliArgsFromVSCodeExecutablePath(vscodeExe, {
      reuseMachineInstall: false,
    });
    const cli = cliArgs[0];
    if (!cli) {
      throw new Error("resolveCliArgsFromVSCodeExecutablePath returned no entries");
    }
    const baseArgs = cliArgs.slice(1);
    for (const vsix of vsixToInstall) {
      const args = [
        ...baseArgs,
        "--user-data-dir",
        userDataDir,
        "--extensions-dir",
        extensionsDir,
        "--install-extension",
        vsix,
        "--force",
      ];
      await new Promise<void>((resolve, reject) => {
        const child = spawn(cli, args, {
          stdio: "inherit",
          shell: process.platform === "win32",
        });
        child.on("exit", (code: number | null) => {
          if (code === 0) resolve();
          else reject(new Error(`code --install-extension ${vsix} exited ${code ?? "?"}`));
        });
        child.on("error", reject);
      });
    }
  }

  const tracePath = await maybeInstallTraceProxy(extensionsDir, cellName);

  // extensionDevelopmentPath is set to LOCAL_EXTENSION ONLY when the
  // cell explicitly requests the local wrapper (via "local:al-lsp-for-
  // agents" in its extensionIds). For cells that don't request the
  // wrapper we point it at HARNESS_DIR — that directory has a
  // package.json without VS Code extension fields (no engines.vscode,
  // no activationEvents), so VS Code's extension loader ignores it
  // rather than activating any extension. We can't omit the field:
  // @vscode/test-electron's TestOptions type marks it required.
  const devPath = extensionDevelopmentPath ?? HARNESS_DIR;

  try {
    await runTests({
      version: lock.vscode,
      extensionDevelopmentPath: devPath,
      extensionTestsPath: join(HARNESS_DIR, "out", "suite", "index.js"),
      extensionTestsEnv: {
        ...(cell.wrapperEnv ?? {}),
        HARNESS_CELL: cellName,
        HARNESS_FIXTURE: fixture.path,
        HARNESS_OUT: outPath,
        HARNESS_BASELINE_MODE: record ? "record" : "compare",
        ...(tracePath ? { AL_LSP_TRACE_FILE: tracePath } : {}),
      },
      launchArgs: [
        openTarget,
        "--user-data-dir",
        userDataDir,
        "--extensions-dir",
        extensionsDir,
        "--disable-workspace-trust",
        "--disable-telemetry",
        "--disable-updates",
        // AL 18.0.2190758 calls registerMcpServerDefinitionProvider which
        // is a proposed API on VS Code 1.100. Without this flag the
        // extension activation throws and no LSP comes up.
        "--enable-proposed-api",
        "ms-dynamics-smb.al",
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
