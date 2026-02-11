#!/usr/bin/env python3
"""
Test script for AL LSP wrapper against a large Business Central project.
Tests timeout handling and hover null safety on DO.Support-66858.

Skips gracefully if the project directory does not exist.
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
from dataclasses import dataclass
from typing import Optional, List, Tuple

# Paths
REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
GO_WRAPPER = os.path.join(REPO_ROOT, "al-language-server-go-windows", "bin", "al-lsp-wrapper.exe")

# Large project paths
LARGE_PROJECT_ROOT = r"u:\Git\DO.Support-66858"
LARGE_PROJECT_CWD = os.path.join(LARGE_PROJECT_ROOT, "DocumentOutput", "Cloud")

# Test files (relative to LARGE_PROJECT_CWD)
TEST_TABLE_FILE = os.path.join(LARGE_PROJECT_CWD, "Al", "Table", "Table 6175281 CDO Setup.al")
TEST_CODEUNIT_FILE = os.path.join(LARGE_PROJECT_CWD, "Al", "Codeunit", "Codeunit 6175279 CDO Module Manager.al")

# Timeouts for large project
INIT_TIMEOUT = 180  # 3 minutes for initialization
REQUEST_TIMEOUT = 120  # 2 minutes per request
MAX_RECEIVE_RETRIES = 200  # More retries for notification-heavy large projects


@dataclass
class TestResult:
    name: str
    passed: bool
    message: str
    duration: float = 0.0
    response: Optional[dict] = None


class LargeProjectTester:
    def __init__(self, wrapper_path: str):
        self.wrapper_path = wrapper_path
        self.proc: Optional[subprocess.Popen] = None
        self.request_id = 0
        self.results: List[TestResult] = []

    def start(self) -> bool:
        """Start the wrapper process."""
        if not os.path.exists(self.wrapper_path):
            print(f"  ERROR: Wrapper not found at {self.wrapper_path}")
            return False

        try:
            self.proc = subprocess.Popen(
                [self.wrapper_path],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                cwd=LARGE_PROJECT_CWD
            )
            return True
        except Exception as e:
            print(f"  ERROR: Failed to start wrapper: {e}")
            return False

    def stop(self):
        """Stop the wrapper process."""
        if self.proc:
            try:
                self.proc.terminate()
                self.proc.wait(timeout=5)
            except:
                self.proc.kill()

    def send(self, msg: dict):
        """Send JSON-RPC message."""
        content = json.dumps(msg)
        message = f"Content-Length: {len(content)}\r\n\r\n{content}"
        self.proc.stdin.write(message.encode("utf-8"))
        self.proc.stdin.flush()

    def receive(self, timeout: float = REQUEST_TIMEOUT) -> Optional[dict]:
        """Receive JSON-RPC message."""
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
        except Exception as e:
            return None

    def request(self, method: str, params: dict, max_retries: int = MAX_RECEIVE_RETRIES) -> Tuple[int, Optional[dict]]:
        """Send request and wait for response."""
        self.request_id += 1
        msg = {
            "jsonrpc": "2.0",
            "id": self.request_id,
            "method": method,
            "params": params
        }
        self.send(msg)

        # Read responses until we get the one we want (skip notifications)
        for _ in range(max_retries):
            response = self.receive()
            if response and response.get("id") == self.request_id:
                return self.request_id, response
            # Received a notification, keep reading

        return self.request_id, None

    def notify(self, method: str, params: dict):
        """Send notification (no response expected)."""
        msg = {
            "jsonrpc": "2.0",
            "method": method,
            "params": params
        }
        self.send(msg)

    def add_result(self, name: str, passed: bool, message: str, duration: float = 0.0, response: dict = None):
        """Add test result."""
        self.results.append(TestResult(name, passed, message, duration, response))

    def test_initialization(self) -> bool:
        """Test: Initialize LSP connection against large project."""
        start_time = time.time()
        root_uri = Path(LARGE_PROJECT_CWD).as_uri()

        _, response = self.request("initialize", {
            "processId": os.getpid(),
            "rootPath": LARGE_PROJECT_CWD,
            "rootUri": root_uri,
            "capabilities": {
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
        })

        duration = time.time() - start_time

        if not response or "result" not in response:
            self.add_result("Initialization", False, "No response or error", duration)
            return False

        self.notify("initialized", {})

        # Wait for project to load - the wrapper handles this internally on first request,
        # but give it a moment to start processing
        print(f"    Waiting for project to load (this may take 1-2 minutes)...")
        time.sleep(5)

        self.add_result("Initialization", True, f"LSP initialized successfully", duration)
        return True

    def test_document_symbol(self):
        """Test: documentSymbol on CDO Setup table."""
        if not os.path.exists(TEST_TABLE_FILE):
            self.add_result("DocumentSymbol", False, f"Test file not found: {TEST_TABLE_FILE}")
            return

        file_uri = Path(TEST_TABLE_FILE).as_uri()
        start_time = time.time()

        _, response = self.request("textDocument/documentSymbol", {
            "textDocument": {"uri": file_uri}
        })

        duration = time.time() - start_time

        if not response:
            self.add_result("DocumentSymbol", False, f"No response (timeout after {duration:.1f}s)", duration)
            return

        if "error" in response:
            self.add_result("DocumentSymbol", False,
                          f"Error: {response['error'].get('message', 'unknown')}", duration)
            return

        if "result" in response and response["result"]:
            symbols = response["result"]
            if isinstance(symbols, list) and len(symbols) > 0:
                self.add_result("DocumentSymbol", True,
                              f"Found {len(symbols)} symbol(s)", duration, response)
                return

        self.add_result("DocumentSymbol", False, "Empty or null result", duration, response)

    def test_hover(self):
        """Test: hover on a known procedure in CDO Module Manager."""
        if not os.path.exists(TEST_CODEUNIT_FILE):
            self.add_result("Hover", False, f"Test file not found: {TEST_CODEUNIT_FILE}")
            return

        file_uri = Path(TEST_CODEUNIT_FILE).as_uri()
        start_time = time.time()

        # Hover on line 1, character 10 - should be near the codeunit name
        _, response = self.request("textDocument/hover", {
            "textDocument": {"uri": file_uri},
            "position": {"line": 1, "character": 10}
        })

        duration = time.time() - start_time

        if not response:
            self.add_result("Hover", False, f"No response (timeout after {duration:.1f}s)", duration)
            return

        if "error" in response:
            self.add_result("Hover", False,
                          f"Error: {response['error'].get('message', 'unknown')}", duration)
            return

        if "result" in response:
            result = response["result"]
            if result is None:
                # Null result is acceptable (no hover info at position)
                self.add_result("Hover", True, "Null result (no hover info at position)", duration)
                return

            contents = result.get("contents")
            if contents is None:
                # This should NOT happen after our fix - contents should never be null
                # in a non-null hover response
                self.add_result("Hover", False,
                              "Hover result has null contents (would crash Claude Code)", duration, response)
                return

            if isinstance(contents, dict) and "kind" in contents:
                self.add_result("Hover", True,
                              f"Valid MarkupContent (kind={contents['kind']})", duration, response)
                return

            self.add_result("Hover", False,
                          f"Invalid contents format (missing 'kind'): {type(contents)}", duration, response)
            return

        self.add_result("Hover", False, "No result in response", duration, response)

    def test_hover_null_safe(self):
        """Test: hover on whitespace position - verify null or valid MarkupContent (no crash)."""
        if not os.path.exists(TEST_TABLE_FILE):
            self.add_result("Hover (null-safe)", False, f"Test file not found: {TEST_TABLE_FILE}")
            return

        file_uri = Path(TEST_TABLE_FILE).as_uri()
        start_time = time.time()

        # Hover on line 0, character 0 — likely whitespace or empty
        _, response = self.request("textDocument/hover", {
            "textDocument": {"uri": file_uri},
            "position": {"line": 0, "character": 0}
        })

        duration = time.time() - start_time

        if not response:
            self.add_result("Hover (null-safe)", False,
                          f"No response (timeout after {duration:.1f}s)", duration)
            return

        if "error" in response:
            self.add_result("Hover (null-safe)", False,
                          f"Error: {response['error'].get('message', 'unknown')}", duration)
            return

        if "result" in response:
            result = response["result"]
            if result is None:
                # Null result is perfectly fine
                self.add_result("Hover (null-safe)", True,
                              "Null result (safe)", duration)
                return

            contents = result.get("contents")
            if contents is None:
                # This would crash Claude Code - FAIL
                self.add_result("Hover (null-safe)", False,
                              "Non-null hover with null contents (would crash Claude Code!)", duration, response)
                return

            if isinstance(contents, dict) and "kind" in contents:
                self.add_result("Hover (null-safe)", True,
                              f"Valid MarkupContent even on whitespace", duration, response)
                return

            self.add_result("Hover (null-safe)", False,
                          f"Invalid contents format: {contents}", duration, response)
            return

        self.add_result("Hover (null-safe)", False, "No result in response", duration, response)

    def test_definition(self):
        """Test: go-to-definition on a symbol reference."""
        if not os.path.exists(TEST_CODEUNIT_FILE):
            self.add_result("Definition", False, f"Test file not found: {TEST_CODEUNIT_FILE}")
            return

        file_uri = Path(TEST_CODEUNIT_FILE).as_uri()
        start_time = time.time()

        # Try definition on line 1, character 10 (near codeunit name)
        _, response = self.request("textDocument/definition", {
            "textDocument": {"uri": file_uri},
            "position": {"line": 1, "character": 10}
        })

        duration = time.time() - start_time

        if not response:
            self.add_result("Definition", False,
                          f"No response (timeout after {duration:.1f}s)", duration)
            return

        if "error" in response:
            self.add_result("Definition", False,
                          f"Error: {response['error'].get('message', 'unknown')}", duration)
            return

        if "result" in response:
            result = response["result"]
            if result is None:
                self.add_result("Definition", True,
                              "Null result (no definition at position)", duration)
                return

            if isinstance(result, list) and len(result) > 0:
                self.add_result("Definition", True,
                              f"Found {len(result)} location(s)", duration, response)
                return
            elif isinstance(result, dict) and "uri" in result:
                self.add_result("Definition", True, "Found definition", duration, response)
                return

        self.add_result("Definition", False, "Empty or null result", duration, response)

    def run_all_tests(self):
        """Run all tests."""
        print(f"\n{'='*60}")
        print(f"Large Project Test Suite")
        print(f"{'='*60}")
        print(f"Project: {LARGE_PROJECT_CWD}")
        print(f"Wrapper: {self.wrapper_path}")

        if not self.start():
            return

        try:
            if not self.test_initialization():
                print("  FATAL: Initialization failed")
                return

            self.test_document_symbol()
            self.test_hover()
            self.test_hover_null_safe()
            self.test_definition()

        finally:
            self.stop()

    def print_results(self):
        """Print test results."""
        print(f"\n--- Large Project Results ---")
        passed = 0
        failed = 0

        for result in self.results:
            status = "PASS" if result.passed else "FAIL"
            icon = "[+]" if result.passed else "[X]"
            timing = f" ({result.duration:.1f}s)" if result.duration > 0 else ""
            print(f"  {icon} {status}: {result.name} - {result.message}{timing}")
            if result.passed:
                passed += 1
            else:
                failed += 1

        print(f"\n  Total: {passed} passed, {failed} failed")
        return passed, failed


def show_log():
    """Show wrapper log (last 50 lines)."""
    # Find the most recent log file
    import glob
    temp_dir = os.environ.get("TEMP", "/tmp")
    pattern = os.path.join(temp_dir, "al-lsp-wrapper-go-*.log")
    log_files = glob.glob(pattern)

    if not log_files:
        print("\n--- No wrapper log files found ---")
        return

    # Sort by modification time, most recent first
    log_files.sort(key=os.path.getmtime, reverse=True)
    log_path = log_files[0]

    if os.path.exists(log_path):
        print(f"\n--- Wrapper Log: {log_path} (last 50 lines) ---")
        with open(log_path) as f:
            lines = f.readlines()
            print("".join(lines[-50:]))


def main():
    import argparse
    parser = argparse.ArgumentParser(description="Test AL LSP wrapper against large project")
    parser.add_argument("--show-logs", action="store_true",
                        help="Show wrapper logs after tests")
    parser.add_argument("--wrapper", default=GO_WRAPPER,
                        help=f"Path to wrapper executable (default: {GO_WRAPPER})")
    args = parser.parse_args()

    # Check if large project exists
    if not os.path.exists(LARGE_PROJECT_CWD):
        print(f"SKIP: Large project not found at {LARGE_PROJECT_CWD}")
        print("This test requires the DO.Support-66858 repository.")
        sys.exit(0)

    # Check if app.json exists
    app_json = os.path.join(LARGE_PROJECT_CWD, "app.json")
    if not os.path.exists(app_json):
        print(f"SKIP: app.json not found at {app_json}")
        sys.exit(0)

    tester = LargeProjectTester(args.wrapper)
    tester.run_all_tests()
    tester.print_results()

    if args.show_logs:
        show_log()

    print("\n" + "=" * 60)
    print("Test Complete")
    print("=" * 60)


if __name__ == "__main__":
    main()
