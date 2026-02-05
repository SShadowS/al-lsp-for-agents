#!/usr/bin/env python3
"""
Test hover response normalization.

Verifies that the Go wrapper normalizes deprecated hover content formats
(MarkedString, plain string, array) to MarkupContent format:
  { kind: "markdown"|"plaintext", value: "..." }

Claude Code's LSP client requires MarkupContent format and crashes with:
  'undefined is not an Object. (evaluating "kind" in H)'
when receiving deprecated formats.

Usage:
    python test_hover_normalization.py
    python test_hover_normalization.py --show-logs
    python test_hover_normalization.py --verbose
"""

import json
import subprocess
import sys
import os
import time
import io

# Fix Windows console encoding for emoji/unicode
if sys.platform == 'win32':
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')
    sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8', errors='replace')

from pathlib import Path

# Paths
REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
GO_WRAPPER = os.path.join(REPO_ROOT, "al-language-server-go", "bin", "al-lsp-wrapper.exe")
TEST_PROJECT = os.path.join(REPO_ROOT, "test-al-project")
CUSTOMER_TABLE = os.path.join(TEST_PROJECT, "src", "Tables", "Customer.Table.al")
CUSTOMER_MGT = os.path.join(TEST_PROJECT, "src", "Codeunits", "CustomerMgt.Codeunit.al")


class LSPClient:
    """Minimal LSP client for testing."""

    def __init__(self, wrapper_path: str):
        self.wrapper_path = wrapper_path
        self.proc = None
        self.request_id = 0

    def start(self) -> bool:
        if not os.path.exists(self.wrapper_path):
            print(f"  ERROR: Wrapper not found at {self.wrapper_path}")
            return False
        try:
            self.proc = subprocess.Popen(
                [self.wrapper_path],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                cwd=TEST_PROJECT
            )
            return True
        except Exception as e:
            print(f"  ERROR: Failed to start wrapper: {e}")
            return False

    def stop(self):
        if self.proc:
            try:
                self.proc.terminate()
                self.proc.wait(timeout=5)
            except:
                self.proc.kill()

    def send(self, msg: dict):
        content = json.dumps(msg)
        message = f"Content-Length: {len(content)}\r\n\r\n{content}"
        self.proc.stdin.write(message.encode("utf-8"))
        self.proc.stdin.flush()

    def receive(self, timeout: float = 30):
        try:
            headers = {}
            while True:
                line = self.proc.stdout.readline().decode("utf-8")
                if not line or line == "\r\n":
                    break
                if ":" in line:
                    key, value = line.split(":", 1)
                    headers[key.strip()] = value.strip()

            if "Content-Length" not in headers:
                return None

            content_length = int(headers["Content-Length"])
            content = self.proc.stdout.read(content_length).decode("utf-8")
            return json.loads(content)
        except Exception:
            return None

    def request(self, method: str, params: dict, max_retries: int = 50):
        self.request_id += 1
        msg = {
            "jsonrpc": "2.0",
            "id": self.request_id,
            "method": method,
            "params": params
        }
        self.send(msg)

        for _ in range(max_retries):
            response = self.receive()
            if response and response.get("id") == self.request_id:
                return response
        return None

    def notify(self, method: str, params: dict):
        msg = {
            "jsonrpc": "2.0",
            "method": method,
            "params": params
        }
        self.send(msg)

    def initialize(self) -> bool:
        root_uri = Path(TEST_PROJECT).as_uri()
        response = self.request("initialize", {
            "processId": os.getpid(),
            "rootPath": TEST_PROJECT,
            "rootUri": root_uri,
            "capabilities": {
                "textDocument": {
                    "hover": {"dynamicRegistration": True, "contentFormat": ["markdown", "plaintext"]},
                    "definition": {"dynamicRegistration": True},
                    "references": {"dynamicRegistration": True},
                    "documentSymbol": {"dynamicRegistration": True}
                },
                "workspace": {
                    "symbol": {"dynamicRegistration": True}
                }
            }
        })
        if not response or "result" not in response:
            return False
        self.notify("initialized", {})
        time.sleep(3)
        return True


def validate_markup_content(contents, verbose=False) -> tuple:
    """
    Validate that contents is in MarkupContent format.
    Returns (is_valid, description).
    """
    if contents is None:
        return False, "contents is null"

    if isinstance(contents, str):
        return False, f"contents is plain string (deprecated): {contents[:80]}"

    if isinstance(contents, list):
        return False, f"contents is array (deprecated): {len(contents)} items"

    if isinstance(contents, dict):
        if "kind" in contents and "value" in contents:
            kind = contents["kind"]
            value = contents["value"]
            if kind in ("markdown", "plaintext"):
                preview = value[:120].replace('\n', '\\n')
                return True, f"MarkupContent(kind={kind}, value={preview}...)"
            else:
                return False, f"MarkupContent with unknown kind: {kind}"

        if "language" in contents and "value" in contents:
            return False, f"contents is MarkedString (deprecated): language={contents['language']}"

        return False, f"contents is dict but missing 'kind': keys={list(contents.keys())}"

    return False, f"contents has unexpected type: {type(contents).__name__}"


def run_hover_test(client, file_path, line, character, label, verbose=False):
    """Run a single hover test and validate the response format."""
    file_uri = Path(file_path).as_uri()

    response = client.request("textDocument/hover", {
        "textDocument": {"uri": file_uri},
        "position": {"line": line, "character": character}
    })

    if not response:
        return False, "No response (timeout)"

    if "error" in response:
        error = response["error"]
        return False, f"LSP error: {error.get('message', 'unknown')} (code: {error.get('code', '?')})"

    result = response.get("result")
    if result is None:
        return None, "null result (no hover info at this position)"

    contents = result.get("contents") if isinstance(result, dict) else None
    if contents is None:
        return None, "no contents in hover result (AL LSP has no info for this position)"

    is_valid, description = validate_markup_content(contents, verbose)

    if verbose:
        print(f"    Raw response: {json.dumps(result, indent=2)[:500]}")

    return is_valid, description


# Define hover test cases: (file, line, character, label, expect_content)
# expect_content: True = must have content, False = null is OK, None = either is fine
HOVER_TESTS = [
    # The known problematic symbol - variable declaration with Codeunit type
    # This was the original bug: returned MarkedString format causing Claude Code crash
    (CUSTOMER_TABLE, 187, 8, "CustomerMgt variable declaration", True),
    (CUSTOMER_TABLE, 187, 30, "Codeunit type in var declaration", True),

    # Field declarations - AL LSP doesn't provide hover for field() declarations
    (CUSTOMER_TABLE, 7, 14, "Field 'No.' declaration", False),
    (CUSTOMER_TABLE, 21, 14, "Field 'Name' declaration", False),

    # Procedure names (0-indexed lines)
    (CUSTOMER_TABLE, 189, 18, "UpdateSearchName procedure", None),
    (CUSTOMER_TABLE, 194, 18, "CheckCreditLimit procedure", None),

    # Record variable usage in a codeunit (0-indexed)
    (CUSTOMER_MGT, 21, 30, "Procedure signature parameter", None),

    # Trigger (0-indexed)
    (CUSTOMER_TABLE, 157, 12, "OnInsert trigger", None),
]


def main():
    import argparse
    parser = argparse.ArgumentParser(description="Test hover response normalization")
    parser.add_argument("--show-logs", action="store_true", help="Show wrapper logs after tests")
    parser.add_argument("--verbose", "-v", action="store_true", help="Show raw response data")
    args = parser.parse_args()

    print("=" * 60)
    print("Hover Normalization Test")
    print("=" * 60)
    print(f"Wrapper: {GO_WRAPPER}")
    print(f"Test project: {TEST_PROJECT}")
    print()

    client = LSPClient(GO_WRAPPER)
    if not client.start():
        sys.exit(1)

    passed = 0
    failed = 0
    skipped = 0

    try:
        print("Initializing LSP...")
        if not client.initialize():
            print("  FATAL: Initialization failed")
            sys.exit(1)
        print("  OK\n")

        print("Running hover tests:")
        print("-" * 60)

        for file_path, line, char, label, expect_content in HOVER_TESTS:
            is_valid, description = run_hover_test(client, file_path, line, char, label, args.verbose)

            # is_valid: True=valid MarkupContent, False=invalid format, None=null result
            if is_valid is None:
                # Null result - no hover info at this position
                if expect_content is True:
                    print(f"  [X] FAIL: {label} (line {line}:{char})")
                    print(f"         Expected content but got: {description}")
                    failed += 1
                else:
                    print(f"  [-] SKIP: {label} (line {line}:{char})")
                    print(f"         {description}")
                    skipped += 1
            elif is_valid:
                print(f"  [+] PASS: {label} (line {line}:{char})")
                print(f"         {description}")
                passed += 1
            else:
                print(f"  [X] FAIL: {label} (line {line}:{char})")
                print(f"         {description}")
                failed += 1

        print("-" * 60)
        print(f"\nResults: {passed} passed, {failed} failed, {skipped} skipped")

        if failed > 0:
            print("\nFAILED - Some hover responses are not in MarkupContent format!")
            print("Claude Code will crash with: 'undefined is not an Object'")
        else:
            print("\nAll hover responses use MarkupContent format.")

    finally:
        client.stop()

    if args.show_logs:
        log_path = os.path.join(os.environ.get("TEMP", "/tmp"), "al-lsp-wrapper-go.log")
        if os.path.exists(log_path):
            print(f"\n--- Wrapper Log (last 20 lines) ---")
            with open(log_path) as f:
                lines = f.readlines()
                print("".join(lines[-20:]))

    print("\n" + "=" * 60)
    print("Test Complete")
    print("=" * 60)

    sys.exit(1 if failed > 0 else 0)


if __name__ == "__main__":
    main()
