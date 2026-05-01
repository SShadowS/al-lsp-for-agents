import { strict as assert } from "node:assert";
import { describe, it } from "mocha";
import { sha256OfFile, verifySha256 } from "../scripts/fetch-vsix.js";
import { writeFile, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

describe("fetch-vsix helpers", () => {
  it("computes sha256 of a file", async () => {
    const dir = await mkdtemp(join(tmpdir(), "harness-test-"));
    try {
      const path = join(dir, "x.bin");
      await writeFile(path, "hello world");
      const hex = await sha256OfFile(path);
      assert.equal(
        hex,
        "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
      );
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });

  it("verifySha256 returns true on match (case-insensitive)", async () => {
    const dir = await mkdtemp(join(tmpdir(), "harness-test-"));
    try {
      const path = join(dir, "x.bin");
      await writeFile(path, "hello world");
      assert.equal(
        await verifySha256(
          path,
          "B94D27B9934D3E08A52E52D7DA7DABFAC484EFE37A5380EE9088F7ACE2EFCDE9"
        ),
        true
      );
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });

  it("verifySha256 returns false on mismatch", async () => {
    const dir = await mkdtemp(join(tmpdir(), "harness-test-"));
    try {
      const path = join(dir, "x.bin");
      await writeFile(path, "hello world");
      assert.equal(
        await verifySha256(path, "0".repeat(64)),
        false
      );
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });
});
