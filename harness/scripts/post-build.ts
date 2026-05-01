/**
 * Post-build script: stamp compiled output directories with CJS package.json
 * files so Node.js treats them as CommonJS.
 *
 * The harness package.json has "type": "module" so tsx can run .ts source
 * files as ESM. But VS Code's extension host loads extensionTestsPath via
 * require(), which rejects ESM. The compiled suite (and its lib/cells deps)
 * must be CJS. Node.js picks the nearest package.json to determine module
 * type, so we place "type": "commonjs" package.json files in each output
 * subdirectory that VS Code will require() into.
 */
import { mkdir, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const HARNESS_DIR = dirname(dirname(fileURLToPath(import.meta.url)));
const OUT = join(HARNESS_DIR, "out");

const DIRS = ["suite", "lib", "cells"];
for (const dir of DIRS) {
  const pkgPath = join(OUT, dir, "package.json");
  await mkdir(dirname(pkgPath), { recursive: true });
  await writeFile(pkgPath, '{ "type": "commonjs" }\n');
  process.stderr.write(`wrote ${pkgPath}\n`);
}
