"""Agent-flow scenario: hover on event-name literal returns publisher
signature.

Validates the wrapper's `enrichEventReferenceHover` overlay end-to-end.
The fixture has an `[EventSubscriber(...)]` attribute referencing
`OnSendPurchaseDocForApproval` on Codeunit 1535 with a deliberately
minimal local procedure signature. The agent's job is to hover on the
event-name string literal to learn the publisher's real signature.

If the overlay works the hover response should contain:
  * the publisher's full signature (parameter types like `Record ...`,
    `Boolean`, etc.)
  * an attribute-kind tag (e.g. `IntegrationEvent`)
  * the source app/codeunit context

Without the overlay the agent just sees AL LSP's stock hover for the
codeunit reference and has no event-specific info — that's the failure
mode this scenario catches.

Run from repo root:
    python test-al-project/agent-flows/subscribe_to_event.py
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

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
    "In `src/Codeunits/ApprovalsMgmtSubDemo.Codeunit.al`, line 9 contains an "
    "`[EventSubscriber(...)]` attribute referencing the event name "
    "`'OnSendPurchaseDocForApproval'`. Use the LSP `hover` operation on the "
    "event-name string literal (line 9, character around 90 — inside the "
    "single quotes) to learn the publisher's signature, then tell me: "
    "(1) what parameters the publisher passes, (2) what attribute kind it is "
    "(IntegrationEvent, BusinessEvent, or InternalEvent), and (3) which app "
    "the publisher lives in. Use the LSP tool only; do not search the "
    "filesystem."
)

# Loose token check on the hover-overlay output. The wrapper's overlay
# adds Publisher/Source/attribute-kind/signature blocks; final answer
# should at minimum mention either the attribute kind or the publisher
# block since those are the overlay's unique additions over AL LSP's
# stock hover.
OVERLAY_TOKENS = [
    "IntegrationEvent",
    "BusinessEvent",
    "Publisher",
    "Source",
]

# AL type tokens we expect in any real BC event signature.
AL_TYPE_TOKENS = [
    "Record",
    "Boolean",
    "var ",
]


def assert_hover_was_used(run: CapturedRun) -> None:
    hovers = run.lsp_calls_for("hover")
    if not hovers:
        raise AssertionFailed(
            "Agent never called LSP hover. Got LSP ops: "
            + ", ".join(sorted({c.input.get("operation", "?") for c in run.lsp_calls()}))
        )


def assert_overlay_visible_in_hover_or_answer(run: CapturedRun) -> None:
    """Either the hover result preview or the agent's final answer must
    show signals that the wrapper's overlay fired. Without the overlay
    the agent sees only "Codeunit System.Automation.Approvals Mgmt." in
    stock AL LSP output, which trivially contains no IntegrationEvent /
    Publisher / Source tokens."""
    haystacks = [c.result for c in run.lsp_calls_for("hover")]
    haystacks.append(run.joined_answer())
    combined = "\n".join(haystacks)
    found = [t for t in OVERLAY_TOKENS if t in combined]
    if not found:
        raise AssertionFailed(
            "Wrapper's hover overlay tokens (IntegrationEvent / "
            "BusinessEvent / Publisher / Source) absent from both the "
            "hover result and the final answer. Overlay likely didn't "
            "fire (al-call-hierarchy didn't index the publisher, or "
            "cursor position didn't land inside the EventSubscriber "
            "attribute's literal). "
            f"Answer excerpt: {truncate(run.joined_answer(), 300)}"
        )


def assert_answer_mentions_signature_types(run: CapturedRun) -> None:
    """The agent's final answer should report concrete AL types from
    the event signature (Record, Boolean, var). A free-form "I don't
    know" reply would lack these — and would indicate the overlay
    didn't supply enough info."""
    answer = run.joined_answer()
    found = [t for t in AL_TYPE_TOKENS if re.search(re.escape(t), answer, re.IGNORECASE)]
    if len(found) < 2:
        raise AssertionFailed(
            f"Final answer mentioned only {found} AL type tokens; "
            f"expected at least 2 (e.g. Record, Boolean, var). Looks "
            f"like the agent didn't get a usable signature. "
            f"Excerpt: {truncate(answer, 300)}"
        )


ASSERTIONS = [
    ("LSP tool was used", assert_lsp_used),
    ("Agent did not thrash on denied tools", make_assert_minimal_denied_attempts()),
    ("Hover was called", assert_hover_was_used),
    ("Wrapper overlay tokens visible (IntegrationEvent / Publisher / etc.)",
     assert_overlay_visible_in_hover_or_answer),
    ("Final answer mentions concrete AL signature types",
     assert_answer_mentions_signature_types),
]


if __name__ == "__main__":
    sys.exit(run_and_report("subscribe_to_event", PROMPT, ASSERTIONS))
