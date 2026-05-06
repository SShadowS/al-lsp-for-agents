"""Integration test: spawn the wrapper with AL_LSP_TELEMETRY_DUMP and
verify the dumped envelope (if any) is well-formed and contains no
forbidden strings.

Phase 1 currently only emits wrapper.panic events from goroutine recover
handlers, which this test does not reliably trigger. So the test mostly
verifies (a) the wrapper accepts the dump-mode env var, (b) any output
that lands is JSON-shaped, (c) nothing leaks home dir or workspace name.
"""

import json
import os
import re
import subprocess
import sys
import tempfile
import time
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
WRAPPER = REPO_ROOT / "al-language-server-go-windows" / "bin" / "al-lsp-wrapper.exe"

GUID_RE = re.compile(r"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}")


def main() -> int:
    if not WRAPPER.exists():
        print(f"FAIL: wrapper binary not found at {WRAPPER}")
        return 2

    forbidden = [
        os.path.expanduser("~"),                       # home dir
        os.path.basename(str(REPO_ROOT)),              # workspace name (e.g., "claude-code-lsps-telemetry")
        "test-al-project",                             # this folder name; should appear scrubbed
    ]

    with tempfile.NamedTemporaryFile(
        suffix=".jsonl", delete=False, mode="w"
    ) as tmp:
        dump_path = tmp.name

    env = os.environ.copy()
    env["AL_LSP_TELEMETRY_DUMP"] = dump_path
    env["AL_LSP_TELEMETRY"] = "errors"
    # Avoid auto-download AL ext during this test; force the wrapper to
    # bail early on a malformed initialize.
    env.pop("AL_LSP_AUTO_DOWNLOAD_AL_EXTENSION", None)

    print(f"Spawning wrapper with dump path: {dump_path}")
    proc = subprocess.Popen(
        [str(WRAPPER), "--launcher", "claude-code"],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        env=env,
    )
    # Send a malformed initialize so the wrapper exits quickly; this
    # exercises the JSON-RPC parsing path without requiring AL extension.
    proc.stdin.write(b"Content-Length: 5\r\n\r\nbogus")
    proc.stdin.flush()
    proc.stdin.close()
    try:
        proc.wait(timeout=15)
    except subprocess.TimeoutExpired:
        proc.kill()
        print("WARN: wrapper did not exit within 15s; killed")

    # Read dump file. Phase 1 will rarely produce events here; the test is
    # primarily a privacy contract check.
    found_events = 0
    leak_failures: list[str] = []
    schema_failures: list[str] = []
    try:
        with open(dump_path, "r", encoding="utf-8") as f:
            for raw_line in f:
                line = raw_line.strip()
                if not line:
                    continue
                found_events += 1
                # Parse as JSON
                try:
                    ev = json.loads(line)
                except json.JSONDecodeError as e:
                    schema_failures.append(f"non-JSON line: {e}; line={line!r}")
                    continue
                # Schema sanity
                if ev.get("schemaVersion") != 1:
                    schema_failures.append(f"bad schemaVersion: {ev.get('schemaVersion')}")
                if not ev.get("sessionId"):
                    schema_failures.append("missing sessionId")
                if "consentLevel" not in ev:
                    schema_failures.append("missing consentLevel")
                # Forbidden substring check (case-insensitive)
                lower = line.lower()
                for forb in forbidden:
                    if forb and forb.lower() in lower:
                        leak_failures.append(f"forbidden substring {forb!r} in line {line!r}")
                # GUID-shape leak check (excluding sessionId field)
                # Mask sessionId before scanning
                masked = re.sub(r'"sessionId"\s*:\s*"[^"]*"', '"sessionId":"<sid>"', line)
                # Allow guid placeholders like "<guid:abcd1234>"
                # The leak check is: any RAW guid outside the sessionId field
                guid_hits = GUID_RE.findall(masked)
                if guid_hits:
                    leak_failures.append(f"unmasked GUID(s) {guid_hits!r} in line {line!r}")
    finally:
        try:
            os.unlink(dump_path)
        except OSError:
            pass

    print(f"Events captured in dump: {found_events}")
    if leak_failures:
        print("PRIVACY FAILURES:")
        for f in leak_failures:
            print(f"  - {f}")
    if schema_failures:
        print("SCHEMA FAILURES:")
        for f in schema_failures:
            print(f"  - {f}")
    if leak_failures or schema_failures:
        return 1
    print("Integration test passed (no leaks, schema clean).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
