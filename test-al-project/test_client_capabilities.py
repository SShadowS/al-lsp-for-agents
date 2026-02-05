#!/usr/bin/env python3
"""
Client capabilities snapshot test.

Compares LSP client capabilities against a known baseline to detect when
Claude Code's capabilities change.

Usage:
    # Compare test client capabilities against baseline
    python test_client_capabilities.py

    # Extract capabilities from wrapper log and compare
    python test_client_capabilities.py --from-log <logfile>

    # Update baseline after verification
    python test_client_capabilities.py --from-log <logfile> --update-baseline

    # Just show current capabilities without comparison
    python test_client_capabilities.py --show-only
"""

import argparse
import json
import os
import re
import sys
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

# Paths
SCRIPT_DIR = Path(__file__).parent
BASELINE_FILE = SCRIPT_DIR / ".snapshots" / "claude-code-client-capabilities.json"


def load_baseline() -> Dict[str, Any]:
    """Load the baseline capabilities from the snapshot file."""
    if not BASELINE_FILE.exists():
        print(f"ERROR: Baseline file not found: {BASELINE_FILE}")
        sys.exit(1)

    with open(BASELINE_FILE, "r", encoding="utf-8") as f:
        data = json.load(f)

    # Remove metadata for comparison
    return {k: v for k, v in data.items() if not k.startswith("_")}


def save_baseline(capabilities: Dict[str, Any], source: str = "unknown") -> None:
    """Save capabilities as the new baseline."""
    from datetime import datetime

    data = {
        "_meta": {
            "description": "Claude Code LSP client capabilities baseline",
            "captured_from": source,
            "last_updated": datetime.now().strftime("%Y-%m-%d"),
            "notes": "This file tracks the LSP capabilities that Claude Code sends during initialize. Update when capabilities change."
        },
        **capabilities
    }

    BASELINE_FILE.parent.mkdir(parents=True, exist_ok=True)
    with open(BASELINE_FILE, "w", encoding="utf-8") as f:
        json.dump(data, f, indent=2)
        f.write("\n")

    print(f"Baseline updated: {BASELINE_FILE}")


def extract_capabilities_from_log(log_path: str) -> Optional[Dict[str, Any]]:
    """
    Extract client capabilities from a wrapper log file.

    Looks for the "=== CLIENT CAPABILITIES (parsed) ===" marker and extracts
    the JSON block that follows.
    """
    if not os.path.exists(log_path):
        print(f"ERROR: Log file not found: {log_path}")
        return None

    with open(log_path, "r", encoding="utf-8", errors="replace") as f:
        content = f.read()

    # Look for the capabilities marker (use rfind to get the LAST occurrence)
    start_marker = "=== CLIENT CAPABILITIES (parsed) ==="
    end_marker = "=== END CLIENT CAPABILITIES ==="

    start_idx = content.rfind(start_marker)
    if start_idx == -1:
        print("ERROR: Could not find CLIENT CAPABILITIES marker in log")
        print("Make sure the log contains an initialize request with capabilities.")
        return None

    end_idx = content.find(end_marker, start_idx)
    if end_idx == -1:
        print("ERROR: Could not find END CLIENT CAPABILITIES marker in log")
        return None

    # Extract the JSON between markers
    json_start = start_idx + len(start_marker)
    json_text = content[json_start:end_idx]

    # Clean up the JSON text (remove log line prefixes if any)
    lines = json_text.strip().split("\n")
    cleaned_lines = []
    for line in lines:
        # Remove timestamp prefix like "[2026-02-05 12:34:56.789] "
        # The timestamp may or may not have content after it
        match = re.match(r"^\[\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3}\]\s*(.*)$", line)
        if match:
            content = match.group(1)
            if content:  # Only add non-empty lines
                cleaned_lines.append(content)
        elif line.strip():  # Non-timestamped, non-empty line
            cleaned_lines.append(line)

    json_text = "\n".join(cleaned_lines)

    try:
        return json.loads(json_text)
    except json.JSONDecodeError as e:
        print(f"ERROR: Failed to parse capabilities JSON: {e}")
        print("Raw extracted text:")
        print(json_text[:500] + "..." if len(json_text) > 500 else json_text)
        return None


def get_test_client_capabilities() -> Dict[str, Any]:
    """
    Get the capabilities that the test script sends.

    This is what test_lsp_go.py sends during initialize - useful as a reference
    but not the same as what Claude Code sends.
    """
    return {
        "textDocument": {
            "hover": {"dynamicRegistration": True},
            "definition": {"dynamicRegistration": True},
            "references": {"dynamicRegistration": True},
            "documentSymbol": {"dynamicRegistration": True}
        },
        "workspace": {
            "symbol": {"dynamicRegistration": True}
        }
    }


def deep_diff(baseline: Any, current: Any, path: str = "") -> List[Tuple[str, str, Any, Any]]:
    """
    Recursively compare two objects and return differences.

    Returns a list of tuples: (path, change_type, baseline_value, current_value)
    change_type is one of: "added", "removed", "changed"
    """
    diffs = []

    if type(baseline) != type(current):
        diffs.append((path or "root", "changed", baseline, current))
        return diffs

    if isinstance(baseline, dict):
        all_keys = set(baseline.keys()) | set(current.keys())
        for key in sorted(all_keys):
            key_path = f"{path}.{key}" if path else key
            if key not in baseline:
                diffs.append((key_path, "added", None, current[key]))
            elif key not in current:
                diffs.append((key_path, "removed", baseline[key], None))
            else:
                diffs.extend(deep_diff(baseline[key], current[key], key_path))
    elif isinstance(baseline, list):
        if baseline != current:
            diffs.append((path or "root", "changed", baseline, current))
    else:
        if baseline != current:
            diffs.append((path or "root", "changed", baseline, current))

    return diffs


def format_diff(diffs: List[Tuple[str, str, Any, Any]]) -> str:
    """Format the diff list as a human-readable string."""
    if not diffs:
        return "No differences found."

    lines = []
    for path, change_type, old_val, new_val in diffs:
        if change_type == "added":
            lines.append(f"  + {path}: {json.dumps(new_val)}")
        elif change_type == "removed":
            lines.append(f"  - {path}: {json.dumps(old_val)}")
        else:
            lines.append(f"  ~ {path}:")
            lines.append(f"      was: {json.dumps(old_val)}")
            lines.append(f"      now: {json.dumps(new_val)}")

    return "\n".join(lines)


def compare_capabilities(baseline: Dict[str, Any], current: Dict[str, Any]) -> List[Tuple[str, str, Any, Any]]:
    """Compare current capabilities against baseline and return differences."""
    return deep_diff(baseline, current)


def main():
    parser = argparse.ArgumentParser(
        description="Compare LSP client capabilities against baseline",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__
    )
    parser.add_argument(
        "--from-log",
        metavar="LOGFILE",
        help="Extract capabilities from wrapper log file"
    )
    parser.add_argument(
        "--update-baseline",
        action="store_true",
        help="Update the baseline file with current capabilities"
    )
    parser.add_argument(
        "--show-only",
        action="store_true",
        help="Just show current capabilities without comparison"
    )
    parser.add_argument(
        "--test-client",
        action="store_true",
        help="Use test client capabilities (what test_lsp_go.py sends)"
    )
    args = parser.parse_args()

    print("=" * 60)
    print("Client Capabilities Snapshot Test")
    print("=" * 60)

    # Get current capabilities
    if args.from_log:
        print(f"\nExtracting capabilities from: {args.from_log}")
        current = extract_capabilities_from_log(args.from_log)
        source = f"Log file: {args.from_log}"
        if current is None:
            sys.exit(1)
    elif args.test_client:
        print("\nUsing test client capabilities (test_lsp_go.py)")
        current = get_test_client_capabilities()
        source = "Test client (test_lsp_go.py)"
    else:
        print("\nNo source specified. Use --from-log or --test-client")
        print("Example: python test_client_capabilities.py --from-log /path/to/wrapper.log")
        sys.exit(1)

    if args.show_only:
        print("\nCurrent capabilities:")
        print(json.dumps(current, indent=2))
        return

    # Load baseline
    print(f"\nBaseline file: {BASELINE_FILE}")
    baseline = load_baseline()

    # Compare
    print("\nComparing capabilities...")
    diffs = compare_capabilities(baseline, current)

    if not diffs:
        print("\n[PASS] Capabilities match baseline")
        return

    print(f"\n[DIFF] Found {len(diffs)} difference(s):")
    print(format_diff(diffs))

    if args.update_baseline:
        print("\nUpdating baseline...")
        save_baseline(current, source)
        print("[OK] Baseline updated")
    else:
        print("\nTo update baseline, run with --update-baseline")
        sys.exit(1)


if __name__ == "__main__":
    main()
