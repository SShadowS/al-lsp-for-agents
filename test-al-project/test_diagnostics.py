#!/usr/bin/env python3
"""Test script to capture and display code quality diagnostics from al-call-hierarchy."""

import json
import subprocess
import sys
import time
import threading
import os

def read_message(proc):
    """Read a JSON-RPC message from the LSP server."""
    headers = {}
    while True:
        line = proc.stdout.readline()
        if not line:
            return None
        line = line.strip()
        if not line:
            break
        if b':' in line:
            key, value = line.split(b':', 1)
            headers[key.strip().lower()] = value.strip()

    content_length = int(headers.get(b'content-length', 0))
    if content_length == 0:
        return None

    content = proc.stdout.read(content_length)
    return json.loads(content.decode('utf-8'))

def send_message(proc, msg):
    """Send a JSON-RPC message to the LSP server."""
    content = json.dumps(msg)
    message = f"Content-Length: {len(content)}\r\n\r\n{content}"
    proc.stdin.write(message.encode('utf-8'))
    proc.stdin.flush()

def format_diagnostic(diag, file_uri):
    """Format a diagnostic for display."""
    severity_map = {1: "Error", 2: "Warning", 3: "Information", 4: "Hint"}
    severity = severity_map.get(diag.get('severity', 3), "Unknown")
    code = diag.get('code', 'unknown')
    message = diag.get('message', '')
    source = diag.get('source', '')
    start_line = diag.get('range', {}).get('start', {}).get('line', 0) + 1

    # Extract just filename from URI
    filename = file_uri.split('/')[-1]

    return f"  [{severity}] {code} (line {start_line})\n    {message}\n    Source: {source}"

def main():
    # Paths
    project_path = os.path.dirname(os.path.abspath(__file__))
    wrapper_path = os.path.join(os.path.dirname(project_path),
                                 "al-language-server-go-windows", "bin", "al-lsp-wrapper.exe")
    test_file = os.path.join(project_path, "src", "Codeunits", "CodeQualityTest.Codeunit.al")

    print("=" * 70)
    print("Code Quality Diagnostics Test")
    print("=" * 70)
    print(f"Wrapper: {wrapper_path}")
    print(f"Test file: {test_file}")
    print()

    # Start the LSP wrapper
    proc = subprocess.Popen(
        [wrapper_path],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        cwd=project_path
    )

    diagnostics_received = {}
    stop_reading = False

    def read_responses():
        """Background thread to read responses and notifications."""
        while not stop_reading:
            try:
                msg = read_message(proc)
                if msg is None:
                    break

                method = msg.get('method', '')
                if method == 'textDocument/publishDiagnostics':
                    params = msg.get('params', {})
                    uri = params.get('uri', '')
                    diags = params.get('diagnostics', [])

                    # Filter to only al-call-hierarchy diagnostics
                    al_diags = [d for d in diags if d.get('source') == 'al-call-hierarchy']
                    if al_diags:
                        diagnostics_received[uri] = al_diags

            except Exception as e:
                if not stop_reading:
                    print(f"Error reading: {e}")
                break

    reader_thread = threading.Thread(target=read_responses, daemon=True)
    reader_thread.start()

    try:
        # Initialize
        file_uri = f"file:///{test_file.replace(os.sep, '/').replace(':', '%3A')}"

        init_msg = {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "initialize",
            "params": {
                "processId": os.getpid(),
                "rootUri": f"file:///{project_path.replace(os.sep, '/').replace(':', '%3A')}",
                "capabilities": {
                    "textDocument": {
                        "publishDiagnostics": {"relatedInformation": True}
                    }
                },
                "workspaceFolders": [{
                    "uri": f"file:///{project_path.replace(os.sep, '/').replace(':', '%3A')}",
                    "name": "test-al-project"
                }]
            }
        }
        send_message(proc, init_msg)
        time.sleep(2)  # Wait for initialization

        # Send initialized notification
        send_message(proc, {"jsonrpc": "2.0", "method": "initialized", "params": {}})
        time.sleep(1)

        # Open the test file
        with open(test_file, 'r') as f:
            content = f.read()

        open_msg = {
            "jsonrpc": "2.0",
            "method": "textDocument/didOpen",
            "params": {
                "textDocument": {
                    "uri": file_uri,
                    "languageId": "al",
                    "version": 1,
                    "text": content
                }
            }
        }
        send_message(proc, open_msg)

        # Wait for diagnostics
        print("Waiting for diagnostics from al-call-hierarchy...")
        print()

        # Wait up to 10 seconds for diagnostics
        for _ in range(20):
            time.sleep(0.5)
            if diagnostics_received:
                break

        # Display results
        if diagnostics_received:
            for uri, diags in diagnostics_received.items():
                filename = uri.split('/')[-1]
                print(f"Diagnostics for {filename}:")
                print("-" * 50)

                # Group by code
                by_code = {}
                for d in diags:
                    code = d.get('code', 'unknown')
                    if code not in by_code:
                        by_code[code] = []
                    by_code[code].append(d)

                for code, code_diags in sorted(by_code.items()):
                    print(f"\n[{code}] ({len(code_diags)} occurrence(s)):")
                    for d in code_diags:
                        print(format_diagnostic(d, uri))
                print()
        else:
            print("No diagnostics received from al-call-hierarchy")
            print("(This might mean the file has no quality issues or the server didn't respond)")

        # Shutdown
        send_message(proc, {"jsonrpc": "2.0", "id": 99, "method": "shutdown", "params": None})
        time.sleep(0.5)
        send_message(proc, {"jsonrpc": "2.0", "method": "exit", "params": None})

    finally:
        stop_reading = True
        proc.terminate()
        try:
            proc.wait(timeout=2)
        except:
            proc.kill()

if __name__ == "__main__":
    main()
