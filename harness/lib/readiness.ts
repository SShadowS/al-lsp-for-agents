/**
 * Quiet-period tracker for "system has settled" detection.
 *
 * Used by diagnostic capture to decide when AL extensions have stopped
 * emitting new diagnostics. Keeps test runs deterministic without a fixed
 * sleep that's either too short (flaky) or too long (slow CI).
 *
 * Conditions for "settled":
 *   - At least minElapsedMs have passed since the start (default 0).
 *   - At least quietMs have passed since the LAST activity.
 *   - OR maxMs total have elapsed (timeout fallback).
 */
export interface QuietPeriodOptions {
  /** Quiet window after last activity. */
  quietMs: number;
  /** Hard upper bound; settle even if still receiving activity. */
  maxMs: number;
  /** Floor: don't settle before this many ms have passed since start. */
  minElapsedMs?: number;
}

export class QuietPeriodTracker {
  private readonly opts: Required<QuietPeriodOptions>;
  private readonly startedAt = 0;
  private lastActivity: number | null = null;

  constructor(opts: QuietPeriodOptions) {
    this.opts = {
      quietMs: opts.quietMs,
      maxMs: opts.maxMs,
      minElapsedMs: opts.minElapsedMs ?? 0,
    };
  }

  markActivity(now: number): void {
    this.lastActivity = now;
  }

  /**
   * Returns the timestamp at which the system first becomes "settled",
   * or null if not settled yet at `now`.
   */
  settledAt(now: number): number | null {
    if (now - this.startedAt < this.opts.minElapsedMs) return null;
    if (now - this.startedAt >= this.opts.maxMs) return now;
    if (this.lastActivity === null) return null;
    if (now - this.lastActivity >= this.opts.quietMs) return now;
    return null;
  }
}

/**
 * Wait for an activity stream to settle. The waiter installs a timer
 * and resolves when the tracker reports settled, polling every pollMs.
 */
export async function waitForSettled(
  tracker: QuietPeriodTracker,
  pollMs = 100
): Promise<void> {
  return new Promise((resolve) => {
    const tick = (): void => {
      if (tracker.settledAt(Date.now()) !== null) {
        resolve();
        return;
      }
      setTimeout(tick, pollMs);
    };
    tick();
  });
}
