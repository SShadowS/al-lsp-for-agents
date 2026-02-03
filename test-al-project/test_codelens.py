#!/usr/bin/env python3
"""Test script to check Code Lens output including parameter counts."""

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

def main():
    # Paths
    project_path = os.path.dirname(os.path.abspath(__file__))
    wrapper_path = os.path.join(os.path.dirname(project_path),
                                 "al-language-server-go", "bin", "al-lsp-wrapper.exe")
    test_file = os.path.join(project_path, "src", "Codeunits", "CodeQualityTest.Codeunit.al")

    print("=" * 70)
    print("Code Lens Test (checking parameter counts)")
    print("=" * 70)
    print(f"Test file: {test_file}")
    print()

    # Start the LSP wrapper with logging enabled
    log_file = os.path.join(project_path, "wrapper_test.log")
    proc = subprocess.Popen(
        [wrapper_path, "--log", log_file],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        cwd=project_path
    )

    responses = {}
    stop_reading = False

    def read_responses():
        while not stop_reading:
            try:
                msg = read_message(proc)
                if msg is None:
                    break
                msg_id = msg.get('id')
                if msg_id:
                    responses[msg_id] = msg
            except Exception as e:
                if not stop_reading:
                    print(f"Error reading: {e}")
                break

    reader_thread = threading.Thread(target=read_responses, daemon=True)
    reader_thread.start()

    try:
        file_uri = f"file:///{test_file.replace(os.sep, '/').replace(':', '%3A')}"

        # Initialize
        init_msg = {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "initialize",
            "params": {
                "processId": os.getpid(),
                "rootUri": f"file:///{project_path.replace(os.sep, '/').replace(':', '%3A')}",
                "capabilities": {},
                "workspaceFolders": [{
                    "uri": f"file:///{project_path.replace(os.sep, '/').replace(':', '%3A')}",
                    "name": "test-al-project"
                }]
            }
        }
        send_message(proc, init_msg)
        time.sleep(2)

        send_message(proc, {"jsonrpc": "2.0", "method": "initialized", "params": {}})
        time.sleep(1)

        # Open the test file
        with open(test_file, 'r') as f:
            content = f.read()

        send_message(proc, {
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
        })
        time.sleep(5)  # Wait longer for al-call-hierarchy to index

        # Request Code Lens
        send_message(proc, {
            "jsonrpc": "2.0",
            "id": 10,
            "method": "textDocument/codeLens",
            "params": {
                "textDocument": {"uri": file_uri}
            }
        })

        # Wait for response
        for _ in range(20):
            time.sleep(0.5)
            if 10 in responses:
                break

        if 10 in responses:
            resp = responses[10]
            if 'error' in resp:
                print(f"Error: {resp['error']}")
            else:
                result = resp.get('result', [])
                if result is None:
                    print("Result is null (no Code Lens support or no items)")
                else:
                    print(f"Found {len(result)} Code Lens items:\n")
                    for lens in result:
                        command = lens.get('command', {})
                        title = command.get('title', 'N/A')
                        line = lens.get('range', {}).get('start', {}).get('line', 0) + 1
                        print(f"  Line {line}: {title}")
        else:
            print("No Code Lens response received")
            print(f"Available responses: {list(responses.keys())}")

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

    # Print last lines of log
    print("\n--- Wrapper Log (last 30 lines) ---")
    if os.path.exists(log_file):
        with open(log_file, 'r') as f:
            lines = f.readlines()
            for line in lines[-30:]:
                print(line.rstrip())
    else:
        print("Log file not found")

if __name__ == "__main__":
    main()
