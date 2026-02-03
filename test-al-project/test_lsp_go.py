#!/usr/bin/env python3
"""
Comprehensive test script for AL LSP wrappers.
Tests both Python and Go implementations and compares results.
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
from typing import Optional, Any, List, Tuple

# Paths
REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PYTHON_WRAPPER = os.path.join(REPO_ROOT, "al-language-server-python", "al_lsp_wrapper.py")
GO_WRAPPER = os.path.join(REPO_ROOT, "al-language-server-go", "bin", "al-lsp-wrapper.exe")
TEST_PROJECT = os.path.join(REPO_ROOT, "test-al-project")
TEST_FILE = os.path.join(TEST_PROJECT, "src", "Tables", "Customer.Table.al")


@dataclass
class TestResult:
    name: str
    passed: bool
    message: str
    response: Optional[dict] = None


class LSPTester:
    def __init__(self, wrapper_path: str, wrapper_type: str):
        self.wrapper_path = wrapper_path
        self.wrapper_type = wrapper_type
        self.proc: Optional[subprocess.Popen] = None
        self.request_id = 0
        self.results: List[TestResult] = []

    def start(self) -> bool:
        """Start the wrapper process."""
        if not os.path.exists(self.wrapper_path):
            print(f"  ERROR: Wrapper not found at {self.wrapper_path}")
            return False

        if self.wrapper_type == "python":
            cmd = [sys.executable, self.wrapper_path]
        else:
            cmd = [self.wrapper_path]

        try:
            self.proc = subprocess.Popen(
                cmd,
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

    def receive(self, timeout: float = 30) -> Optional[dict]:
        """Receive JSON-RPC message."""
        import select

        # Simple blocking read with implicit timeout via process
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

    def request(self, method: str, params: dict, max_retries: int = 50) -> Tuple[int, Optional[dict]]:
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
        # Need many retries because AL LSP sends lots of notifications during project load
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

    def add_result(self, name: str, passed: bool, message: str, response: dict = None):
        """Add test result."""
        self.results.append(TestResult(name, passed, message, response))

    def initialize(self) -> bool:
        """Initialize the LSP connection."""
        root_uri = Path(TEST_PROJECT).as_uri()

        _, response = self.request("initialize", {
            "processId": os.getpid(),
            "rootPath": TEST_PROJECT,
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

        if not response or "result" not in response:
            self.add_result("Initialize", False, "No response or error")
            return False

        self.notify("initialized", {})
        time.sleep(3)  # Wait for project to load

        self.add_result("Initialize", True, "LSP initialized successfully")
        return True

    def test_hover(self) -> TestResult:
        """Test textDocument/hover."""
        file_uri = Path(TEST_FILE).as_uri()

        _, response = self.request("textDocument/hover", {
            "textDocument": {"uri": file_uri},
            "position": {"line": 187, "character": 30}  # On "CustomerMgt"
        })

        if not response:
            return self.add_result("Hover", False, "No response (timeout)")

        if "error" in response:
            return self.add_result("Hover", False, f"Error: {response['error'].get('message', 'unknown')}")

        if "result" in response and response["result"]:
            contents = response["result"].get("contents", {})
            if contents:
                return self.add_result("Hover", True, "Got hover information", response)

        return self.add_result("Hover", False, "Empty or null result", response)

    def test_definition(self) -> TestResult:
        """Test textDocument/definition."""
        file_uri = Path(TEST_FILE).as_uri()

        _, response = self.request("textDocument/definition", {
            "textDocument": {"uri": file_uri},
            "position": {"line": 76, "character": 45}  # On "CustomerType" enum
        })

        if not response:
            return self.add_result("Definition", False, "No response (timeout)")

        if "error" in response:
            return self.add_result("Definition", False, f"Error: {response['error'].get('message', 'unknown')}")

        if "result" in response and response["result"]:
            result = response["result"]
            # Could be a single location or array of locations
            if isinstance(result, list) and len(result) > 0:
                return self.add_result("Definition", True, f"Found {len(result)} location(s)", response)
            elif isinstance(result, dict) and "uri" in result:
                return self.add_result("Definition", True, "Found definition", response)

        return self.add_result("Definition", False, "Empty or null result", response)

    def test_document_symbol(self) -> TestResult:
        """Test textDocument/documentSymbol."""
        file_uri = Path(TEST_FILE).as_uri()

        _, response = self.request("textDocument/documentSymbol", {
            "textDocument": {"uri": file_uri}
        })

        if not response:
            return self.add_result("DocumentSymbol", False, "No response (timeout)")

        if "error" in response:
            return self.add_result("DocumentSymbol", False, f"Error: {response['error'].get('message', 'unknown')}")

        if "result" in response and response["result"]:
            symbols = response["result"]
            if isinstance(symbols, list) and len(symbols) > 0:
                return self.add_result("DocumentSymbol", True, f"Found {len(symbols)} symbol(s)", response)

        return self.add_result("DocumentSymbol", False, "Empty or null result", response)

    def test_workspace_symbol(self) -> TestResult:
        """Test workspace/symbol."""
        _, response = self.request("workspace/symbol", {
            "query": "Customer"
        })

        if not response:
            return self.add_result("WorkspaceSymbol", False, "No response (timeout)")

        if "error" in response:
            return self.add_result("WorkspaceSymbol", False, f"Error: {response['error'].get('message', 'unknown')}")

        if "result" in response and response["result"]:
            symbols = response["result"]
            if isinstance(symbols, list) and len(symbols) > 0:
                return self.add_result("WorkspaceSymbol", True, f"Found {len(symbols)} symbol(s)", response)

        return self.add_result("WorkspaceSymbol", False, "Empty or null result", response)

    def test_workspace_symbol_empty_query(self) -> TestResult:
        """Test workspace/symbol with empty query (should return error)."""
        _, response = self.request("workspace/symbol", {
            "query": ""
        })

        if not response:
            return self.add_result("WorkspaceSymbol (empty)", False, "No response (timeout)")

        if "error" in response:
            # This is expected behavior
            return self.add_result("WorkspaceSymbol (empty)", True, "Correctly returned error for empty query", response)

        return self.add_result("WorkspaceSymbol (empty)", False, "Should have returned error for empty query", response)

    def test_workspace_symbol_path_workaround(self) -> TestResult:
        """Test workspace/symbol with file path as query (workaround test)."""
        _, response = self.request("workspace/symbol", {
            "query": "C:/some/path/Customer.Table.al"
        })

        if not response:
            return self.add_result("WorkspaceSymbol (path)", False, "No response (timeout)")

        if "error" in response:
            return self.add_result("WorkspaceSymbol (path)", False, f"Error: {response['error'].get('message', 'unknown')}")

        if "result" in response:
            # Should have extracted "Customer" from the path
            return self.add_result("WorkspaceSymbol (path)", True, "Path workaround worked", response)

        return self.add_result("WorkspaceSymbol (path)", False, "Unexpected response", response)

    def test_references(self) -> TestResult:
        """Test textDocument/references."""
        file_uri = Path(TEST_FILE).as_uri()

        _, response = self.request("textDocument/references", {
            "textDocument": {"uri": file_uri},
            "position": {"line": 187, "character": 30},  # On "CustomerMgt"
            "context": {"includeDeclaration": True}
        })

        if not response:
            return self.add_result("References", False, "No response (timeout)")

        if "error" in response:
            return self.add_result("References", False, f"Error: {response['error'].get('message', 'unknown')}")

        if "result" in response:
            refs = response["result"]
            if refs is None:
                return self.add_result("References", True, "No references found (null result)", response)
            if isinstance(refs, list):
                return self.add_result("References", True, f"Found {len(refs)} reference(s)", response)

        return self.add_result("References", False, "Unexpected response format", response)

    def test_call_hierarchy(self) -> TestResult:
        """Test textDocument/prepareCallHierarchy on a procedure."""
        # Test on TestNoSeries procedure in CustomerMgt.Codeunit.al (line 52)
        codeunit_file = os.path.join(TEST_PROJECT, "src", "Codeunits", "CustomerMgt.Codeunit.al")
        file_uri = Path(codeunit_file).as_uri()

        _, response = self.request("textDocument/prepareCallHierarchy", {
            "textDocument": {"uri": file_uri},
            "position": {"line": 52, "character": 14}  # On "TestNoSeries" procedure name
        })

        if not response:
            return self.add_result("CallHierarchy", False, "No response (timeout)")

        if "error" in response:
            error_code = response["error"].get("code", 0)
            if error_code == -32601:  # Method not found
                return self.add_result("CallHierarchy", False, "Method not supported (MethodNotFound)", response)
            return self.add_result("CallHierarchy", False, f"Error (code: {error_code}): {response['error'].get('message', 'unknown')}", response)

        if "result" in response and response["result"]:
            items = response["result"]
            if isinstance(items, list) and len(items) > 0:
                return self.add_result("CallHierarchy", True, f"Found {len(items)} call hierarchy item(s)", response)

        return self.add_result("CallHierarchy", False, "Empty or null result", response)

    def test_code_lens(self) -> TestResult:
        """Test textDocument/codeLens on a codeunit file."""
        # Use CodeQualityTest.Codeunit.al which has procedures with high complexity and many params
        codeunit_file = os.path.join(TEST_PROJECT, "src", "Codeunits", "CodeQualityTest.Codeunit.al")
        file_uri = Path(codeunit_file).as_uri()

        _, response = self.request("textDocument/codeLens", {
            "textDocument": {"uri": file_uri}
        })

        if not response:
            return self.add_result("CodeLens", False, "No response (timeout)")

        if "error" in response:
            error_code = response["error"].get("code", 0)
            return self.add_result("CodeLens", False, f"Error (code: {error_code}): {response['error'].get('message', 'unknown')}", response)

        if "result" in response:
            items = response["result"]
            if items is None:
                return self.add_result("CodeLens", False, "Result is null")
            if isinstance(items, list):
                if len(items) > 0:
                    # Show first few items
                    sample = items[:3]
                    titles = [item.get('command', {}).get('title', 'N/A') for item in sample]
                    return self.add_result("CodeLens", True, f"Found {len(items)} lens items (e.g., {titles})", response)
                else:
                    return self.add_result("CodeLens", False, "Empty result array")

        return self.add_result("CodeLens", False, "Unexpected result format", response)

    def run_all_tests(self):
        """Run all tests."""
        print(f"\n{'='*60}")
        print(f"Testing {self.wrapper_type.upper()} wrapper")
        print(f"{'='*60}")
        print(f"Wrapper: {self.wrapper_path}")

        if not self.start():
            return

        try:
            if not self.initialize():
                print("  FATAL: Initialization failed")
                return

            # Run all tests
            self.test_hover()
            self.test_definition()
            self.test_document_symbol()
            self.test_workspace_symbol()
            self.test_workspace_symbol_empty_query()
            self.test_workspace_symbol_path_workaround()
            self.test_references()
            self.test_call_hierarchy()
            self.test_code_lens()

        finally:
            self.stop()

    def print_results(self):
        """Print test results."""
        print(f"\n--- {self.wrapper_type.upper()} Results ---")
        passed = 0
        failed = 0

        for result in self.results:
            status = "PASS" if result.passed else "FAIL"
            icon = "[+]" if result.passed else "[X]"
            print(f"  {icon} {status}: {result.name} - {result.message}")
            if result.passed:
                passed += 1
            else:
                failed += 1

        print(f"\n  Total: {passed} passed, {failed} failed")
        return passed, failed


def compare_results(python_tester: LSPTester, go_tester: LSPTester):
    """Compare results between Python and Go wrappers."""
    print(f"\n{'='*60}")
    print("Comparison")
    print(f"{'='*60}")

    python_results = {r.name: r for r in python_tester.results}
    go_results = {r.name: r for r in go_tester.results}

    all_tests = set(python_results.keys()) | set(go_results.keys())

    matches = 0
    mismatches = 0

    for test_name in sorted(all_tests):
        py = python_results.get(test_name)
        go = go_results.get(test_name)

        if py and go:
            if py.passed == go.passed:
                status = "MATCH"
                matches += 1
            else:
                status = "DIFFER"
                mismatches += 1
            print(f"  {test_name}:")
            print(f"    Python: {'PASS' if py.passed else 'FAIL'}")
            print(f"    Go:     {'PASS' if go.passed else 'FAIL'}")
            print(f"    Status: {status}")
        elif py:
            print(f"  {test_name}: Only Python tested")
        else:
            print(f"  {test_name}: Only Go tested")

    print(f"\n  Matching: {matches}, Differing: {mismatches}")


def show_log(wrapper_type: str):
    """Show wrapper log."""
    if wrapper_type == "python":
        log_path = os.path.join(os.environ.get("TEMP", "/tmp"), "al-lsp-wrapper.log")
    else:
        log_path = os.path.join(os.environ.get("TEMP", "/tmp"), "al-lsp-wrapper-go.log")

    if os.path.exists(log_path):
        print(f"\n--- {wrapper_type.upper()} Wrapper Log (last 30 lines) ---")
        with open(log_path) as f:
            lines = f.readlines()
            print("".join(lines[-30:]))


def main():
    import argparse
    parser = argparse.ArgumentParser(description="Test AL LSP wrappers")
    parser.add_argument("--wrapper", choices=["python", "go", "both"], default="go",
                        help="Which wrapper to test (default: go)")
    parser.add_argument("--show-logs", action="store_true",
                        help="Show wrapper logs after tests")
    parser.add_argument("--verbose", "-v", action="store_true",
                        help="Show detailed response data")
    args = parser.parse_args()

    print("=" * 60)
    print("AL LSP Wrapper Test Suite")
    print("=" * 60)
    print(f"Test project: {TEST_PROJECT}")
    print(f"Test file: {TEST_FILE}")

    python_tester = None
    go_tester = None

    if args.wrapper in ("python", "both"):
        python_tester = LSPTester(PYTHON_WRAPPER, "python")
        python_tester.run_all_tests()
        py_passed, py_failed = python_tester.print_results()
        if args.show_logs:
            show_log("python")

    if args.wrapper in ("go", "both"):
        go_tester = LSPTester(GO_WRAPPER, "go")
        go_tester.run_all_tests()
        go_passed, go_failed = go_tester.print_results()
        if args.show_logs:
            show_log("go")

    if args.wrapper == "both" and python_tester and go_tester:
        compare_results(python_tester, go_tester)

    print("\n" + "=" * 60)
    print("Test Complete")
    print("=" * 60)


if __name__ == "__main__":
    main()
