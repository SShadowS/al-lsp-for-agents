/**
 * Refresh sha256 hashes in extensions.lock.json. Downloads each pinned
 * extension, computes sha256, writes back to the lock file. Optionally
 * bumps a single extension to the latest available version (--bump <id>).
 *
 * Usage:
 *   tsx scripts/refresh-lock.ts                   # rehash all
 *   tsx scripts/refresh-lock.ts --bump <ext-id>   # bump one to "latest"
 */

import { readFile, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { request } from "node:https";
import type { ExtensionLockFile } from "../lib/types.js";
import { downloadVsix, sha256OfFile, vsixCachePath } from "./fetch-vsix.js";
import { splitExtensionId } from "../lib/marketplace.js";

const HARNESS_DIR = dirname(dirname(fileURLToPath(import.meta.url)));
const LOCK_PATH = join(HARNESS_DIR, "extensions.lock.json");

async function loadLock(): Promise<ExtensionLockFile> {
  return JSON.parse(await readFile(LOCK_PATH, "utf8")) as ExtensionLockFile;
}

async function saveLock(lock: ExtensionLockFile): Promise<void> {
  await writeFile(LOCK_PATH, JSON.stringify(lock, null, 2) + "\n");
}

/**
 * Query Marketplace for the latest version of an extension.
 * Uses the gallery query API.
 */
async function queryLatestVersion(id: string): Promise<string> {
  const { publisher, name } = splitExtensionId(id);
  const body = JSON.stringify({
    filters: [
      {
        criteria: [
          { filterType: 7, value: `${publisher}.${name}` },
        ],
      },
    ],
    flags: 103,
  });
  const data = await new Promise<string>((resolve, reject) => {
    const req = request(
      "https://marketplace.visualstudio.com/_apis/public/gallery/extensionquery",
      {
        method: "POST",
        headers: {
          Accept: "application/json;api-version=3.0-preview.1",
          "Content-Type": "application/json",
          "User-Agent": "al-lsp-for-agents-harness/0",
        },
      },
      (res) => {
        const chunks: Buffer[] = [];
        res.on("data", (c: Buffer) => chunks.push(c));
        res.on("end", () => resolve(Buffer.concat(chunks).toString("utf8")));
        res.on("error", reject);
      }
    );
    req.on("error", reject);
    req.write(body);
    req.end();
  });
  const parsed = JSON.parse(data) as {
    results?: Array<{
      extensions?: Array<{ versions?: Array<{ version: string }> }>;
    }>;
  };
  const ext = parsed.results?.[0]?.extensions?.[0];
  const version = ext?.versions?.[0]?.version;
  if (!version) {
    throw new Error(`Could not determine latest version for ${id}`);
  }
  return version;
}

async function main(): Promise<void> {
  const args = process.argv.slice(2);
  const lock = await loadLock();

  const bumpIdx = args.indexOf("--bump");
  if (bumpIdx !== -1) {
    const id = args[bumpIdx + 1];
    if (!id) throw new Error("--bump requires an extension id");
    const pin = lock.extensions.find((p) => p.id === id);
    if (!pin) throw new Error(`Unknown extension ${id} in lock file`);
    const newVersion = await queryLatestVersion(id);
    if (newVersion !== pin.version) {
      process.stderr.write(`bumping ${id}: ${pin.version} -> ${newVersion}\n`);
      pin.version = newVersion;
      pin.sha256 = "";
    } else {
      process.stderr.write(`${id} already at latest (${pin.version})\n`);
    }
  }

  for (const pin of lock.extensions) {
    process.stderr.write(`hashing ${pin.id}@${pin.version}... `);
    await downloadVsix(pin);
    const sha = await sha256OfFile(vsixCachePath(pin));
    if (pin.sha256 && pin.sha256 !== sha) {
      process.stderr.write(`changed (was ${pin.sha256.slice(0, 12)})\n`);
    } else if (!pin.sha256) {
      process.stderr.write("recorded\n");
    } else {
      process.stderr.write("unchanged\n");
    }
    pin.sha256 = sha;
  }

  await saveLock(lock);
  process.stderr.write(`wrote ${LOCK_PATH}\n`);
}

const isMain = process.argv[1] === fileURLToPath(import.meta.url);
if (isMain) {
  main().catch((err) => {
    console.error(err.message);
    process.exit(1);
  });
}
