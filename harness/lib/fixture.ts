/**
 * Per-cell fixture management. Each cell gets a fresh copy of the AL
 * fixture under a unique temp dir so writes to .alpackages, .alcache,
 * .altemplates don't leak between cells.
 */

import { cp, rm, mkdtemp, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const HARNESS_DIR = dirname(dirname(fileURLToPath(import.meta.url)));
const REPO_ROOT = dirname(HARNESS_DIR);
const SOURCE_FIXTURE = join(REPO_ROOT, "test-al-project");

const CACHE_DIRS = [".alpackages", ".alcache", ".altemplates", ".vscode-test"];

export interface FixtureCopy {
  /** Absolute path to the fixture copy. */
  path: string;
  /** Disposer that hard-removes the copy. */
  dispose: () => Promise<void>;
}

/**
 * Make a fresh copy of the AL fixture in a temp directory and return its
 * path. Excludes node_modules, .git, and any cache dirs from the source.
 */
export async function makeFixtureCopy(cellName: string): Promise<FixtureCopy> {
  const root = await mkdtemp(join(tmpdir(), `al-harness-${cellName}-`));
  const dest = join(root, "fixture");
  await cp(SOURCE_FIXTURE, dest, {
    recursive: true,
    filter: (src) => {
      const base = src.split(/[/\\]/).pop() ?? "";
      if (base === "node_modules" || base === ".git") return false;
      if (CACHE_DIRS.includes(base)) return false;
      return true;
    },
  });
  return {
    path: dest,
    dispose: async () => {
      await rm(root, { recursive: true, force: true });
    },
  };
}

/**
 * Hard-purge cache directories under a fixture path. Used between phases
 * within a single cell run if needed; per-cell isolation already happens
 * via fresh copies, so this is mostly defensive.
 */
export async function purgeCacheDirs(fixturePath: string): Promise<void> {
  for (const dir of CACHE_DIRS) {
    const target = join(fixturePath, dir);
    try {
      await stat(target);
      await rm(target, { recursive: true, force: true });
    } catch {
      // not present, fine
    }
  }
}
