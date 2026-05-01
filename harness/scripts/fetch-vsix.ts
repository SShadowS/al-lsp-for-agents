/**
 * Download a pinned-version .vsix from the VS Code Marketplace and verify
 * its sha256 against the lock file. Used by `npm run fetch` and indirectly
 * by `npm run test:matrix` (which calls fetch on demand for missing files).
 *
 * Usage:
 *   tsx scripts/fetch-vsix.ts                  # fetch all from lock
 *   tsx scripts/fetch-vsix.ts ms-dynamics-smb.al  # fetch one
 */

import { createHash } from "node:crypto";
import { createReadStream, createWriteStream } from "node:fs";
import { mkdir, readFile, stat, unlink } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { request } from "node:https";
import { createGunzip } from "node:zlib";
import { pipeline } from "node:stream/promises";
import type { ExtensionLockFile, ExtensionPin } from "../lib/types.js";
import { buildVsixUrl } from "../lib/marketplace.js";

const HARNESS_DIR = dirname(dirname(fileURLToPath(import.meta.url)));
const LOCK_PATH = join(HARNESS_DIR, "extensions.lock.json");
const CACHE_DIR = join(HARNESS_DIR, ".cache", "vsix");

export async function loadLock(): Promise<ExtensionLockFile> {
  const data = await readFile(LOCK_PATH, "utf8");
  const parsed = JSON.parse(data) as ExtensionLockFile;
  if (parsed.schema !== 1) {
    throw new Error(`Unsupported lock schema ${parsed.schema}`);
  }
  return parsed;
}

export function vsixCachePath(pin: ExtensionPin): string {
  return join(CACHE_DIR, `${pin.id}-${pin.version}.vsix`);
}

export async function sha256OfFile(path: string): Promise<string> {
  const hash = createHash("sha256");
  await pipeline(createReadStream(path), hash);
  return hash.digest("hex");
}

export async function verifySha256(
  path: string,
  expected: string
): Promise<boolean> {
  if (!expected) return false;
  const actual = await sha256OfFile(path);
  return actual.toLowerCase() === expected.toLowerCase();
}

/**
 * Download a single extension's vsix to the cache. Follows redirects.
 * The Marketplace serves vsix bodies gzip-compressed; this writes the
 * decompressed bytes (the actual vsix zip) to disk.
 */
export async function downloadVsix(pin: ExtensionPin): Promise<string> {
  await mkdir(CACHE_DIR, { recursive: true });
  const target = vsixCachePath(pin);

  // If file exists and sha256 matches, skip.
  try {
    await stat(target);
    if (pin.sha256 && (await verifySha256(target, pin.sha256))) {
      return target;
    }
  } catch {
    // not present, fall through
  }

  const url = buildVsixUrl(pin.id, pin.version);
  await streamUrlToFile(url, target);
  return target;
}

async function streamUrlToFile(
  url: string,
  destination: string,
  redirectsRemaining = 5
): Promise<void> {
  const tmpPath = `${destination}.partial`;
  await new Promise<void>((resolve, reject) => {
    const cleanup = (err: Error) => {
      unlink(tmpPath).catch(() => {});
      reject(err);
    };
    const req = request(
      url,
      {
        method: "GET",
        headers: {
          "User-Agent": "al-lsp-for-agents-harness/0",
          // Marketplace can return either gzip-encoded or already-decoded.
          // Asking for identity simplifies stream handling.
          "Accept-Encoding": "gzip",
        },
      },
      (res) => {
        const status = res.statusCode ?? 0;
        if (status >= 300 && status < 400 && res.headers.location) {
          if (redirectsRemaining <= 0) {
            reject(new Error(`Too many redirects: ${url}`));
            return;
          }
          res.resume();
          const nextUrl = new URL(res.headers.location, url).href;
          streamUrlToFile(
            nextUrl,
            destination,
            redirectsRemaining - 1
          ).then(resolve, reject);
          return;
        }
        if (status !== 200) {
          reject(new Error(`HTTP ${status} for ${url}`));
          return;
        }
        const file = createWriteStream(tmpPath);
        const encoding = res.headers["content-encoding"];
        if (encoding && encoding !== "gzip") {
          cleanup(new Error(`Unsupported content-encoding: ${encoding}`));
          return;
        }
        const upstream = encoding === "gzip" ? res.pipe(createGunzip()) : res;
        upstream.pipe(file);
        file.on("finish", () => file.close(() => resolve()));
        file.on("error", cleanup);
        upstream.on("error", cleanup);
      }
    );
    req.on("error", cleanup);
    req.end();
  });
  // Atomic rename
  const { rename } = await import("node:fs/promises");
  await rename(tmpPath, destination);
}

/**
 * Fetch all pins (or a subset by id), verify sha256.
 * Does NOT mutate the lock file. Use `refresh-lock.ts` for that.
 */
export async function fetchAll(filterIds?: string[]): Promise<void> {
  const lock = await loadLock();
  const pins = filterIds
    ? lock.extensions.filter((p) => filterIds.includes(p.id))
    : lock.extensions;
  if (pins.length === 0 && filterIds) {
    throw new Error(`No matching extensions: ${filterIds.join(", ")}`);
  }
  for (const pin of pins) {
    process.stderr.write(`fetching ${pin.id}@${pin.version}... `);
    const path = await downloadVsix(pin);
    if (pin.sha256) {
      const ok = await verifySha256(path, pin.sha256);
      if (!ok) {
        await unlink(path);
        throw new Error(
          `sha256 mismatch for ${pin.id}@${pin.version}; deleted ${path}. Run 'npm run refresh' if this version was intentionally updated.`
        );
      }
      process.stderr.write("verified\n");
    } else {
      process.stderr.write(
        "downloaded (no sha256 in lock; run 'npm run refresh' to record)\n"
      );
    }
  }
}

// CLI entrypoint
const isMain = process.argv[1] === fileURLToPath(import.meta.url);
if (isMain) {
  const args = process.argv.slice(2);
  fetchAll(args.length ? args : undefined).catch((err) => {
    console.error(err.message);
    process.exit(1);
  });
}
