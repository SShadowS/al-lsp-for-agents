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
const DEFAULT_FIXTURE = join(REPO_ROOT, "test-al-project");
const FIXTURES_ROOT = join(HARNESS_DIR, "fixtures");

const CACHE_DIRS = [".alpackages", ".alcache", ".altemplates", ".vscode-test"];

export interface FixtureCopy {
  /** Absolute path to the fixture copy. */
  path: string;
  /** Disposer that hard-removes the copy. */
  dispose: () => Promise<void>;
}

/**
 * Resolve a fixture name to its source dir.
 * - undefined → default test-al-project at repo root
 * - absolute path or path with drive letter (e.g. "U:\\...") → use as-is
 * - relative path containing "/" or "\\" → resolved against repo root
 *   (lets a cell point at a sibling repo like ../DO.Support-UISimply/Core)
 * - bare name → looked up under harness/fixtures/<name>
 */
export function resolveFixtureSource(fixtureName?: string): string {
  if (!fixtureName) return DEFAULT_FIXTURE;
  const isAbsolute = /^([A-Za-z]:[\\/]|\/)/.test(fixtureName);
  if (isAbsolute) return fixtureName;
  if (fixtureName.includes("/") || fixtureName.includes("\\")) {
    return join(REPO_ROOT, fixtureName);
  }
  return join(FIXTURES_ROOT, fixtureName);
}

/**
 * Make a fresh copy of the AL fixture in a temp directory and return its
 * path. Excludes node_modules, .git, and any cache dirs from the source.
 */
export async function makeFixtureCopy(
  cellName: string,
  fixtureName?: string
): Promise<FixtureCopy> {
  const source = resolveFixtureSource(fixtureName);
  const root = await mkdtemp(join(tmpdir(), `al-harness-${cellName}-`));
  const dest = join(root, "fixture");
  // Variant fixtures (those under harness/fixtures/) ship intentional
  // .alpackages contents — pre-compiled symbol .app files needed for
  // cross-app dependency reproduction. The default fixture
  // (test-al-project) lives in a dir shared with non-harness scripts and
  // can accumulate stale cache state, so its CACHE_DIRS get stripped.
  const stripCache = !fixtureName;
  await cp(source, dest, {
    recursive: true,
    filter: (src) => {
      const base = src.split(/[/\\]/).pop() ?? "";
      if (base === "node_modules" || base === ".git") return false;
      if (stripCache && CACHE_DIRS.includes(base)) return false;
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
