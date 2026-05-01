import { strict as assert } from "node:assert";
import { describe, it } from "mocha";
import { QuietPeriodTracker } from "../lib/readiness.js";

describe("readiness", () => {
  describe("QuietPeriodTracker", () => {
    it("starts unsettled", () => {
      const t = new QuietPeriodTracker({ quietMs: 100, maxMs: 1000 });
      assert.equal(t.settledAt(0), null);
    });

    it("settles after quietMs of no activity", () => {
      const t = new QuietPeriodTracker({ quietMs: 100, maxMs: 1000 });
      t.markActivity(0);
      assert.equal(t.settledAt(50), null);
      assert.equal(t.settledAt(99), null);
      assert.equal(t.settledAt(100), 100);
    });

    it("resets quiet timer on new activity", () => {
      const t = new QuietPeriodTracker({ quietMs: 100, maxMs: 1000 });
      t.markActivity(0);
      assert.equal(t.settledAt(99), null);
      t.markActivity(50); // resets
      assert.equal(t.settledAt(149), null);
      assert.equal(t.settledAt(150), 150);
    });

    it("returns elapsed at maxMs even if not quiet", () => {
      const t = new QuietPeriodTracker({ quietMs: 100, maxMs: 500 });
      // continuous activity every 50ms, never quiet
      for (let i = 0; i <= 500; i += 50) t.markActivity(i);
      assert.equal(t.settledAt(500), 500);
    });

    it("respects minElapsedMs floor", () => {
      const t = new QuietPeriodTracker({
        quietMs: 100,
        maxMs: 1000,
        minElapsedMs: 200,
      });
      t.markActivity(0);
      // quiet at 100 but min floor is 200
      assert.equal(t.settledAt(100), null);
      assert.equal(t.settledAt(200), 200);
    });
  });
});
