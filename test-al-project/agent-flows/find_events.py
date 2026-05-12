"""Agent-flow scenario: enumerate IntegrationEvent publishers in
Codeunit 1535 "Approvals Mgmt." (Base Application) via LSP only.

Validates the al-preview:/ → file:// rewrite path end-to-end:
  goToDefinition resolves to al-preview URI, wrapper materializes
  the .al source from the .app archive, agent reads it via
  documentSymbol on the cache path. The wrapper's content-scan
  fallback + canonical filename matcher must work or the agent
  cannot enumerate events at all.

See _harness.py for shared infrastructure and `README.md` for the
overall test strategy.

Run from repo root:
    python test-al-project/agent-flows/find_events.py
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

# Make sibling module importable without packaging gymnastics.
sys.path.insert(0, str(Path(__file__).resolve().parent))

from _harness import (  # noqa: E402
    AssertionFailed,
    CapturedRun,
    assert_lsp_used,
    make_assert_minimal_denied_attempts,
    run_and_report,
    truncate,
)

PROMPT = (
    "I'm writing event subscribers in `src/Codeunits/ApprovalsMgmtSubs.Codeunit.al`. "
    "Use the AL LSP tools (NOT grep) to find IntegrationEvent publishers in "
    "Codeunit 1535 'Approvals Mgmt.' from the Base Application that I could "
    "subscribe to. Return the event names as a bullet list. Be concise."
)

# Structural assertion: any answer must contain >= N strings matching a
# BC IntegrationEvent shape. Codeunit 1535 has 137 in real BC. Using a
# regex on shape (not a hardcoded list) stays stable as BC upstream
# adds/removes specific events.
EVENT_NAME_RE = re.compile(
    r"\bOn(?:Send|Cancel|Approve|Reject|Delegate|After|Before)"
    r"[A-Z][A-Za-z0-9]+\b"
)
MIN_EVENTS_IN_ANSWER = 5


def assert_documentsymbol_succeeded_on_approvals(run: CapturedRun) -> None:
    """Find at least one documentSymbol call whose result references
    Codeunit 1535's events / methods."""
    ds = run.lsp_calls_for("documentSymbol")
    if not ds:
        raise AssertionFailed("No documentSymbol LSP call observed.")
    for call in ds:
        if call.is_error:
            continue
        body = call.result
        if "IntegrationEvent" in body or re.search(r"OnAfter|OnBefore", body):
            return
    raise AssertionFailed(
        "documentSymbol calls succeeded but no IntegrationEvent symbols "
        "appeared. Result excerpts: "
        + " | ".join(truncate(c.result, 200) for c in ds)
    )


def assert_answer_lists_events(run: CapturedRun) -> None:
    answer = run.joined_answer()
    hits = sorted(set(EVENT_NAME_RE.findall(answer)))
    if len(hits) < MIN_EVENTS_IN_ANSWER:
        raise AssertionFailed(
            f"Final answer mentioned only {len(hits)} BC-event-shaped "
            f"names (threshold {MIN_EVENTS_IN_ANSWER}). Hits: {hits}. "
            f"Excerpt: {truncate(answer, 400)}"
        )


ASSERTIONS = [
    ("LSP tool was used", assert_lsp_used),
    ("Agent did not thrash on denied tools", make_assert_minimal_denied_attempts()),
    ("documentSymbol returned IntegrationEvent symbols",
     assert_documentsymbol_succeeded_on_approvals),
    (f"Final answer lists >={MIN_EVENTS_IN_ANSWER} expected event names",
     assert_answer_lists_events),
]


if __name__ == "__main__":
    sys.exit(run_and_report("find_events", PROMPT, ASSERTIONS))
