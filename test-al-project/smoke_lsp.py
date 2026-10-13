#!/usr/bin/env python3
"""Smoke test: does a wrapper binary start and serve both backends?

Starts the given wrapper on this test project and checks that
  1. initialize answers and advertises codeLensProvider (capability merge)
  2. documentSymbol answers (Microsoft AL LS is up)
  3. codeLens answers (al-sem is up)
  4. shutdown/exit end the process

Exits non-zero on any failure, so CI can gate on it.

    python smoke_lsp.py <wrapper> [wrapper args...]
"""

import json
import os
import queue
import subprocess
import sys
import threading
import time
from pathlib import Path
from typing import NoReturn

PROJECT = os.path.dirname(os.path.abspath(__file__))
TABLE = Path(PROJECT, "src", "Tables", "Customer.Table.al")
CODEUNIT = Path(PROJECT, "src", "Codeunits", "CodeQualityTest.Codeunit.al")
STEP_TIMEOUT = 180  # first run downloads the AL extension


def fail(msg) -> NoReturn:
    print(f"FAIL: {msg}")
    sys.exit(1)


def main():
    if len(sys.argv) < 2:
        fail("usage: smoke_lsp.py <wrapper> [args...]")

    proc = subprocess.Popen(sys.argv[1:], cwd=PROJECT, stdin=subprocess.PIPE,
                            stdout=subprocess.PIPE, stderr=sys.stderr)
    out, stdin = proc.stdout, proc.stdin
    assert out and stdin
    inbox = queue.Queue()

    def reader():
        while True:
            headers = {}
            while True:
                line = out.readline()
                if not line:
                    inbox.put(None)
                    return
                line = line.decode().strip()
                if not line:
                    break
                k, _, v = line.partition(":")
                headers[k.strip().lower()] = v.strip()
            inbox.put(json.loads(out.read(int(headers["content-length"]))))

    threading.Thread(target=reader, daemon=True).start()
    next_id = 0

    def send(msg):
        body = json.dumps({"jsonrpc": "2.0", **msg}).encode()
        stdin.write(b"Content-Length: %d\r\n\r\n" % len(body) + body)
        stdin.flush()

    def request(method, params):
        nonlocal next_id
        next_id += 1
        send({"id": next_id, "method": method, "params": params})
        deadline = time.time() + STEP_TIMEOUT
        while True:
            try:
                msg = inbox.get(timeout=max(0, deadline - time.time()))
            except queue.Empty:
                fail(f"{method}: no response in {STEP_TIMEOUT}s")
            if msg is None:
                fail(f"{method}: wrapper exited (code {proc.poll()})")
            if msg.get("id") == next_id and "method" not in msg:
                if "error" in msg:
                    fail(f"{method}: {msg['error']}")
                return msg.get("result")
            if "method" in msg and "id" in msg:  # server->client request
                send({"id": msg["id"], "result": None})

    def until_nonempty(method, params, what):
        deadline = time.time() + STEP_TIMEOUT
        while time.time() < deadline:
            result = request(method, params)
            if result:
                print(f"ok   {what}: {len(result)} item(s)")
                return
            time.sleep(2)
        fail(f"{what}: still empty after {STEP_TIMEOUT}s")

    try:
        caps = request("initialize", {
            "processId": os.getpid(),
            "rootUri": Path(PROJECT).as_uri(),
            "capabilities": {},
        })["capabilities"]
        if "codeLensProvider" not in caps:
            fail("initialize: codeLensProvider missing from merged capabilities")
        print("ok   initialize")
        send({"method": "initialized", "params": {}})

        for f in (TABLE, CODEUNIT):
            send({"method": "textDocument/didOpen", "params": {"textDocument": {
                "uri": f.as_uri(), "languageId": "al", "version": 1,
                "text": f.read_text(encoding="utf-8")}}})

        until_nonempty("textDocument/documentSymbol",
                       {"textDocument": {"uri": TABLE.as_uri()}}, "documentSymbol (AL LS)")
        until_nonempty("textDocument/codeLens",
                       {"textDocument": {"uri": CODEUNIT.as_uri()}}, "codeLens (al-sem)")

        request("shutdown", None)
        send({"method": "exit"})
        try:
            proc.wait(timeout=30)
        except subprocess.TimeoutExpired:
            fail("wrapper did not exit after shutdown/exit")
        print("ok   shutdown")
    finally:
        if proc.poll() is None:
            proc.kill()


if __name__ == "__main__":
    main()
