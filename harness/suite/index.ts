/**
 * Mocha bootstrap that runs INSIDE VS Code's extension host (loaded by
 * `@vscode/test-electron` via `extensionTestsPath`). Discovers test files
 * in this directory and runs them.
 */
import * as path from "node:path";
import Mocha from "mocha";
import { glob } from "glob";

export async function run(): Promise<void> {
  const mocha = new Mocha({
    ui: "bdd",
    color: false,
    timeout: 180_000, // generous: AL extensions can be slow on cold start
  });

  // __dirname is available in CJS (this file is compiled with module:CommonJS)
  const files = await glob("*.test.js", {
    cwd: __dirname,
    absolute: true,
  });
  for (const f of files) mocha.addFile(f);

  await new Promise<void>((resolve, reject) => {
    mocha.run((failures) => {
      if (failures > 0) reject(new Error(`${failures} test(s) failed`));
      else resolve();
    });
  });
}
