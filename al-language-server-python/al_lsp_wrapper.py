#!/usr/bin/env python3
"""
AL Language Server Wrapper for Claude Code.

This wrapper sits between Claude Code and the AL Language Server, handling:
1. Proper initialization sequence (workspace config, app.json, active workspace)
2. Translation of standard LSP calls to AL-specific calls
3. Response format normalization

Based on the Serena project's AL Language Server implementation.
"""

import atexit
import json
import os
import platform
import subprocess
import sys
import threading
import time
from pathlib import Path
from typing import Any
from urllib.parse import unquote

# Windows Job Object for automatic child process cleanup
# This ensures child processes are terminated when the parent is killed
_job_handle = None


def _create_windows_job_object():
    """Create a Windows Job Object that terminates children when parent dies."""
    global _job_handle
    if platform.system() != "Windows" or _job_handle is not None:
        return

    try:
        import ctypes
        from ctypes import wintypes

        kernel32 = ctypes.windll.kernel32

        # Job object constants
        JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x2000
        JobObjectExtendedLimitInformation = 9

        class JOBOBJECT_BASIC_LIMIT_INFORMATION(ctypes.Structure):
            _fields_ = [
                ("PerProcessUserTimeLimit", ctypes.c_int64),
                ("PerJobUserTimeLimit", ctypes.c_int64),
                ("LimitFlags", wintypes.DWORD),
                ("MinimumWorkingSetSize", ctypes.c_size_t),
                ("MaximumWorkingSetSize", ctypes.c_size_t),
                ("ActiveProcessLimit", wintypes.DWORD),
                ("Affinity", ctypes.POINTER(ctypes.c_ulong)),
                ("PriorityClass", wintypes.DWORD),
                ("SchedulingClass", wintypes.DWORD),
            ]

        class IO_COUNTERS(ctypes.Structure):
            _fields_ = [
                ("ReadOperationCount", ctypes.c_uint64),
                ("WriteOperationCount", ctypes.c_uint64),
                ("OtherOperationCount", ctypes.c_uint64),
                ("ReadTransferCount", ctypes.c_uint64),
                ("WriteTransferCount", ctypes.c_uint64),
                ("OtherTransferCount", ctypes.c_uint64),
            ]

        class JOBOBJECT_EXTENDED_LIMIT_INFORMATION(ctypes.Structure):
            _fields_ = [
                ("BasicLimitInformation", JOBOBJECT_BASIC_LIMIT_INFORMATION),
                ("IoInfo", IO_COUNTERS),
                ("ProcessMemoryLimit", ctypes.c_size_t),
                ("JobMemoryLimit", ctypes.c_size_t),
                ("PeakProcessMemoryUsed", ctypes.c_size_t),
                ("PeakJobMemoryUsed", ctypes.c_size_t),
            ]

        # Create the job object
        _job_handle = kernel32.CreateJobObjectW(None, None)
        if not _job_handle:
            return

        # Set the job to terminate all processes when the handle is closed
        info = JOBOBJECT_EXTENDED_LIMIT_INFORMATION()
        info.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE

        kernel32.SetInformationJobObject(
            _job_handle,
            JobObjectExtendedLimitInformation,
            ctypes.byref(info),
            ctypes.sizeof(info),
        )
    except Exception:
        pass


def _add_process_to_job(process: subprocess.Popen) -> None:
    """Add a process to the Windows Job Object."""
    global _job_handle
    if platform.system() != "Windows" or _job_handle is None:
        return

    try:
        import ctypes

        kernel32 = ctypes.windll.kernel32

        # Get the process handle
        PROCESS_ALL_ACCESS = 0x1F0FFF
        handle = kernel32.OpenProcess(PROCESS_ALL_ACCESS, False, process.pid)
        if handle:
            kernel32.AssignProcessToJobObject(_job_handle, handle)
            kernel32.CloseHandle(handle)
    except Exception:
        pass


# Initialize job object on Windows at module load
_create_windows_job_object()

# Logging to file for debugging
LOG_FILE = os.path.join(os.environ.get("TEMP", "/tmp"), "al-lsp-wrapper.log")


def log(msg: str) -> None:
    """Log message to file."""
    try:
        with open(LOG_FILE, "a", encoding="utf-8") as f:
            f.write(f"{msg}\n")
    except Exception:
        pass


def find_al_extension() -> str | None:
    """Find newest AL extension in VS Code extensions."""
    home = Path.home()
    possible_paths = []

    if platform.system() == "Windows":
        possible_paths = [
            home / ".vscode" / "extensions",
            home / ".vscode-insiders" / "extensions",
        ]
    else:
        possible_paths = [
            home / ".vscode" / "extensions",
            home / ".vscode-server" / "extensions",
            home / ".vscode-insiders" / "extensions",
        ]

    candidates = []
    for base_path in possible_paths:
        if base_path.exists():
            for item in base_path.iterdir():
                if item.is_dir() and item.name.startswith("ms-dynamics-smb.al-"):
                    candidates.append(item)

    if not candidates:
        return None

    # Sort by version (extract version string after "ms-dynamics-smb.al-")
    def version_key(p: Path) -> tuple:
        version_str = p.name.replace("ms-dynamics-smb.al-", "")
        try:
            return tuple(int(x) for x in version_str.split("."))
        except ValueError:
            return (0,)

    candidates.sort(key=version_key, reverse=True)
    return str(candidates[0])


def get_executable_path(extension_path: str) -> str:
    """Get platform-specific executable path."""
    system = platform.system()
    if system == "Windows":
        return os.path.join(extension_path, "bin", "win32", "Microsoft.Dynamics.Nav.EditorServices.Host.exe")
    elif system == "Linux":
        return os.path.join(extension_path, "bin", "linux", "Microsoft.Dynamics.Nav.EditorServices.Host")
    elif system == "Darwin":
        return os.path.join(extension_path, "bin", "darwin", "Microsoft.Dynamics.Nav.EditorServices.Host")
    else:
        raise RuntimeError(f"Unsupported platform: {system}")


def find_al_project(start_path: str, max_depth: int = 5) -> str | None:
    """
    Find AL project root by searching for app.json.

    Searches in the start_path and its subdirectories (up to max_depth).
    Returns the directory containing app.json, or None if not found.
    """
    start = Path(start_path)
    if not start.exists():
        return None

    # First check if app.json is directly in start_path
    if (start / "app.json").exists():
        log(f"Found app.json in: {start}")
        return str(start)

    # Search subdirectories (breadth-first, limited depth)
    for depth in range(1, max_depth + 1):
        pattern = "/".join(["*"] * depth) + "/app.json"
        matches = list(start.glob(pattern))
        if matches:
            # Return the first match's parent directory
            project_path = str(matches[0].parent)
            log(f"Found app.json at depth {depth}: {project_path}")
            return project_path

    log(f"No app.json found in {start_path} (searched {max_depth} levels deep)")
    return None


def find_project_for_file(file_path: str) -> str | None:
    """
    Find AL project root for a specific file by walking UP to find app.json.

    This is used to determine which project a file belongs to when a workspace
    contains multiple AL projects.
    """
    current = Path(file_path).parent
    while current != current.parent:  # Stop at filesystem root
        if (current / "app.json").exists():
            log(f"Found project for file {file_path}: {current}")
            return str(current)
        current = current.parent
    log(f"No project found for file: {file_path}")
    return None


def find_call_hierarchy_executable() -> str | None:
    """Find the al-call-hierarchy executable for the current platform."""
    script_dir = Path(__file__).parent
    system = platform.system()

    if system == "Windows":
        exe_path = script_dir / "bin" / "win32" / "al-call-hierarchy.exe"
    elif system == "Linux":
        exe_path = script_dir / "bin" / "linux" / "al-call-hierarchy"
    elif system == "Darwin":
        exe_path = script_dir / "bin" / "darwin" / "al-call-hierarchy"
    else:
        return None

    if exe_path.exists():
        return str(exe_path)
    return None


class CallHierarchyServer:
    """Manages the al-call-hierarchy subprocess for call hierarchy operations."""

    # Timeout for reading responses (seconds)
    READ_TIMEOUT = 30

    def __init__(self):
        self.process: subprocess.Popen | None = None
        self.request_id = 0
        self.initialized = False
        self.root_uri: str | None = None
        self._stderr_thread: threading.Thread | None = None

    def start(self, executable: str) -> bool:
        """Start the al-call-hierarchy process."""
        try:
            log(f"Starting al-call-hierarchy: {executable}")
            self.process = subprocess.Popen(
                [executable],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )
            # Start thread to drain stderr to prevent blocking
            self._stderr_thread = threading.Thread(
                target=self._drain_stderr, daemon=True
            )
            self._stderr_thread.start()
            # Add to Windows job object for automatic cleanup on parent exit
            _add_process_to_job(self.process)
            # Register cleanup to stop process on exit
            atexit.register(self.shutdown)
            log("al-call-hierarchy process started")
            return True
        except Exception as e:
            log(f"Failed to start al-call-hierarchy: {e}")
            return False

    def _drain_stderr(self) -> None:
        """Drain stderr to prevent subprocess from blocking."""
        if not self.process or not self.process.stderr:
            return
        try:
            for line in self.process.stderr:
                # Log stderr output for debugging
                log(f"al-call-hierarchy stderr: {line.decode('utf-8', errors='replace').strip()}")
        except Exception:
            pass

    def is_alive(self) -> bool:
        """Check if the al-call-hierarchy process is still running."""
        return self.process is not None and self.process.poll() is None

    def stop(self) -> None:
        """Stop the al-call-hierarchy process."""
        if self.process is not None:
            log("Stopping al-call-hierarchy process...")
            try:
                # Try graceful shutdown first
                self.process.terminate()
                try:
                    self.process.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    # Force kill if it doesn't stop
                    log("al-call-hierarchy did not terminate, killing...")
                    self.process.kill()
                    self.process.wait(timeout=1)
                log("al-call-hierarchy process stopped")
            except Exception as e:
                log(f"Error stopping al-call-hierarchy: {e}")
            finally:
                self.process = None
                self.initialized = False

    def send_message(self, msg: dict) -> bool:
        """Send JSON-RPC message to al-call-hierarchy. Returns True on success."""
        if not self.is_alive() or not self.process.stdin:
            log("CallHierarchy: cannot send message, process not alive")
            return False
        try:
            content = json.dumps(msg)
            message = f"Content-Length: {len(content)}\r\n\r\n{content}"
            self.process.stdin.write(message.encode("utf-8"))
            self.process.stdin.flush()
            log(f"CallHierarchy sent: {msg.get('method', msg.get('id', 'response'))}")
            return True
        except Exception as e:
            log(f"CallHierarchy error sending message: {e}")
            return False

    def read_message(self, timeout: float | None = None) -> dict | None:
        """Read JSON-RPC message from al-call-hierarchy with timeout."""
        if not self.is_alive() or not self.process.stdout:
            log("CallHierarchy: cannot read message, process not alive")
            return None

        if timeout is None:
            timeout = self.READ_TIMEOUT

        try:
            import select
            # Use select for timeout on Unix, fall back to blocking on Windows
            if platform.system() != "Windows":
                ready, _, _ = select.select([self.process.stdout], [], [], timeout)
                if not ready:
                    log(f"CallHierarchy: read timeout after {timeout}s")
                    return None

            headers = {}
            while True:
                line = self.process.stdout.readline().decode("utf-8")
                if not line:
                    log("CallHierarchy: EOF while reading headers")
                    return None
                if line == "\r\n":
                    break
                if ":" in line:
                    key, value = line.split(":", 1)
                    headers[key.strip()] = value.strip()

            if "Content-Length" not in headers:
                log("CallHierarchy: missing Content-Length header")
                return None

            content_length = int(headers["Content-Length"])
            content = self.process.stdout.read(content_length).decode("utf-8")
            return json.loads(content)
        except Exception as e:
            log(f"CallHierarchy error reading message: {e}")
            return None

    def send_request(self, method: str, params: dict | None) -> dict | None:
        """Send request and wait for response."""
        if not self.is_alive():
            log(f"CallHierarchy: process not alive, cannot send {method}")
            return None

        self.request_id += 1
        request = {
            "jsonrpc": "2.0",
            "id": self.request_id,
            "method": method,
            "params": params or {},
        }
        if not self.send_message(request):
            return None
        return self.read_message()

    def send_notification(self, method: str, params: dict | None) -> None:
        """Send notification (no response expected)."""
        notification = {
            "jsonrpc": "2.0",
            "method": method,
            "params": params or {},
        }
        self.send_message(notification)

    def initialize(self, root_uri: str, workspace_folders: list[dict]) -> bool:
        """Initialize the al-call-hierarchy server."""
        if self.initialized:
            return True

        self.root_uri = root_uri
        response = self.send_request("initialize", {
            "processId": os.getpid(),
            "capabilities": {},
            "rootUri": root_uri,
            "workspaceFolders": workspace_folders,
        })

        if response and "result" in response:
            caps = response["result"].get("capabilities", {})
            if caps.get("callHierarchyProvider"):
                self.send_notification("initialized", {})
                self.initialized = True
                log("al-call-hierarchy initialized successfully")
                return True
            else:
                log("al-call-hierarchy does not support callHierarchyProvider")
        elif response and "error" in response:
            log(f"al-call-hierarchy initialization error: {response['error']}")
        else:
            log(f"al-call-hierarchy initialization failed: {response}")

        return False

    def request(self, method: str, params: dict | None) -> dict | None:
        """Send a request to al-call-hierarchy and return the response."""
        if not self.is_alive():
            log(f"CallHierarchy: process not alive for {method}")
            return None
        response = self.send_request(method, params)
        if response and "error" in response:
            log(f"CallHierarchy error response for {method}: {response['error']}")
        return response

    def shutdown(self) -> None:
        """Gracefully shutdown the al-call-hierarchy server."""
        if self.process and self.is_alive():
            try:
                # Send LSP shutdown sequence
                self.send_request("shutdown", None)
                self.send_notification("exit", None)
            except Exception as e:
                log(f"Error sending shutdown to al-call-hierarchy: {e}")
        # Always call stop to ensure process is terminated
        self.stop()


class ALLSPWrapper:
    """Wrapper for AL Language Server."""

    def __init__(self):
        self.process: subprocess.Popen | None = None
        self.request_id = 0
        self.pending_requests: dict[int, Any] = {}
        self.initialized = False
        self.root_path: str | None = None
        self.root_uri: str | None = None
        self.workspace_root: str | None = None  # Original workspace root (may contain multiple AL projects)
        self._read_thread: threading.Thread | None = None
        self._running = False
        self.opened_files: set[str] = set()  # Track opened files
        self.initialized_projects: set[str] = set()  # Track initialized project roots
        self.call_hierarchy_server: CallHierarchyServer | None = None

    def start(self, executable: str) -> None:
        """Start the AL LSP process."""
        log(f"Starting AL LSP: {executable}")
        self.process = subprocess.Popen(
            [executable],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        self._running = True
        # Add to Windows job object for automatic cleanup on parent exit
        _add_process_to_job(self.process)
        log("AL LSP process started")

    def send_message(self, msg: dict) -> None:
        """Send JSON-RPC message to AL LSP."""
        if not self.process or not self.process.stdin:
            return
        content = json.dumps(msg)
        message = f"Content-Length: {len(content)}\r\n\r\n{content}"
        self.process.stdin.write(message.encode("utf-8"))
        self.process.stdin.flush()
        log(f"Sent: {msg.get('method', msg.get('id', 'response'))}")

    def read_message(self) -> dict | None:
        """Read JSON-RPC message from AL LSP."""
        if not self.process or not self.process.stdout:
            return None

        try:
            # Read headers
            headers = {}
            while True:
                line = self.process.stdout.readline().decode("utf-8")
                if not line or line == "\r\n":
                    break
                if ":" in line:
                    key, value = line.split(":", 1)
                    headers[key.strip()] = value.strip()

            if "Content-Length" not in headers:
                return None

            # Read content
            content_length = int(headers["Content-Length"])
            content = self.process.stdout.read(content_length).decode("utf-8")
            return json.loads(content)
        except Exception as e:
            log(f"Error reading message: {e}")
            return None

    def send_request(self, method: str, params: dict) -> dict | None:
        """Send request and wait for response."""
        self.request_id += 1
        request = {
            "jsonrpc": "2.0",
            "id": self.request_id,
            "method": method,
            "params": params,
        }
        log(f"Sending to AL LSP: {method} (id={self.request_id})")
        self.send_message(request)

        # Wait for response with matching ID
        log(f"Waiting for response to {method}...")
        while True:
            response = self.read_message()
            if response is None:
                log(f"Got None response for {method}")
                return None
            if response.get("id") == self.request_id:
                log(f"Got response for {method}")
                return response
            # Handle notifications
            if "method" in response and "id" not in response:
                self.handle_notification(response)

    def send_notification(self, method: str, params: dict) -> None:
        """Send notification (no response expected)."""
        notification = {
            "jsonrpc": "2.0",
            "method": method,
            "params": params,
        }
        self.send_message(notification)

    def handle_notification(self, notification: dict) -> None:
        """Handle incoming notification from AL LSP."""
        method = notification.get("method", "")
        log(f"Notification: {method}")

        # Forward diagnostics to Claude Code
        if method == "textDocument/publishDiagnostics":
            params = notification.get("params", {})
            diagnostics = params.get("diagnostics", [])
            uri = params.get("uri", "")
            log(f"Forwarding {len(diagnostics)} diagnostics for {uri}")
            write_client_message(notification)

    def _uri_to_path(self, uri: str) -> str:
        """Convert file URI to local path."""
        file_path = uri.replace("file:///", "").replace("file://", "")
        # Decode URL-encoded characters (e.g., %20 -> space)
        file_path = unquote(file_path)
        if platform.system() == "Windows" and file_path.startswith("/"):
            file_path = file_path[1:]
        return file_path

    def _ensure_file_opened(self, uri: str) -> None:
        """Ensure file is opened with AL LSP before operations."""
        if uri in self.opened_files:
            return

        file_path = self._uri_to_path(uri)
        try:
            with open(file_path, encoding="utf-8") as f:
                content = f.read()

            self.send_notification("textDocument/didOpen", {
                "textDocument": {
                    "uri": uri,
                    "languageId": "al",
                    "version": 1,
                    "text": content,
                }
            })
            self.opened_files.add(uri)
            log(f"Opened file: {file_path}")
        except Exception as e:
            log(f"Failed to open file {file_path}: {e}")

    def _ensure_project_initialized(self, file_uri: str) -> str | None:
        """
        Ensure the AL project for a file is initialized.

        Returns the project root path, or None if no project found.
        """
        file_path = self._uri_to_path(file_uri)
        project_root = find_project_for_file(file_path)

        if not project_root:
            log(f"No project found for {file_path}")
            return None

        # Normalize the path for consistent tracking
        project_root = str(Path(project_root).resolve())

        if project_root in self.initialized_projects:
            log(f"Project already initialized: {project_root}")
            return project_root

        log(f"Initializing project: {project_root}")

        # Send workspace configuration for this project
        self.send_notification(
            "workspace/didChangeConfiguration",
            {
                "settings": {
                    "workspacePath": project_root,
                    "alResourceConfigurationSettings": {
                        "assemblyProbingPaths": ["./.netpackages"],
                        "codeAnalyzers": [],
                        "enableCodeAnalysis": False,
                        "backgroundCodeAnalysis": "Project",
                        "packageCachePaths": ["./.alpackages"],
                        "ruleSetPath": None,
                        "enableCodeActions": True,
                        "incrementalBuild": False,
                        "outputAnalyzerStatistics": True,
                        "enableExternalRulesets": True,
                    },
                    "setActiveWorkspace": True,
                    "expectedProjectReferenceDefinitions": [],
                    "activeWorkspaceClosure": [project_root],
                }
            },
        )

        # Open app.json for this project
        app_json_path = Path(project_root) / "app.json"
        if app_json_path.exists():
            try:
                with open(app_json_path, encoding="utf-8") as f:
                    app_json_content = f.read()

                self.send_notification(
                    "textDocument/didOpen",
                    {
                        "textDocument": {
                            "uri": app_json_path.as_uri(),
                            "languageId": "json",
                            "version": 1,
                            "text": app_json_content,
                        }
                    },
                )
                log(f"Opened app.json for project: {project_root}")
            except Exception as e:
                log(f"Failed to open app.json for {project_root}: {e}")

        # Set as active workspace
        try:
            workspace_uri = Path(project_root).resolve().as_uri()
            self.send_request(
                "al/setActiveWorkspace",
                {
                    "currentWorkspaceFolderPath": {
                        "uri": workspace_uri,
                        "name": Path(project_root).name,
                        "index": 0,
                    },
                    "settings": {
                        "workspacePath": project_root,
                        "setActiveWorkspace": True,
                    },
                },
            )
            log(f"Set active workspace: {project_root}")
        except Exception as e:
            log(f"Failed to set active workspace for {project_root}: {e}")

        # Mark as initialized
        self.initialized_projects.add(project_root)
        log(f"Project initialized: {project_root}")

        return project_root

    def initialize(self, params: dict) -> dict:
        """Handle initialize request with AL-specific setup."""
        # Log raw initialize params from Claude Code to see client capabilities
        log("=== CLIENT INITIALIZE PARAMS (raw) ===")
        log(json.dumps(params, indent=2))
        log("=== END CLIENT INITIALIZE PARAMS ===")

        # Log just the capabilities for easier reading
        if "capabilities" in params:
            log("=== CLIENT CAPABILITIES (parsed) ===")
            log(json.dumps(params["capabilities"], indent=2))
            log("=== END CLIENT CAPABILITIES ===")

        workspace_root = params.get("rootPath") or params.get("rootUri", "").replace("file:///", "").replace("file://", "")

        # Save original workspace root for call hierarchy (may contain multiple AL projects)
        self.workspace_root = workspace_root
        log(f"Workspace root: {self.workspace_root}")

        # Auto-detect AL project by finding app.json
        al_project = find_al_project(workspace_root) if workspace_root else None

        if al_project:
            self.root_path = al_project
            self.root_uri = Path(al_project).as_uri()
            log(f"Auto-detected AL project: {self.root_path}")
        else:
            # Fallback to workspace root
            self.root_path = workspace_root
            self.root_uri = params.get("rootUri")
            if not self.root_uri and self.root_path:
                self.root_uri = Path(self.root_path).as_uri()
            log(f"No AL project found, using workspace root: {self.root_path}")

        log(f"Initialize with root: {self.root_path}")

        # Build AL-specific initialize params
        al_params = self._build_initialize_params(params)

        # Send to AL LSP
        response = self.send_request("initialize", al_params)

        if response and "result" in response:
            self.initialized = True
            # Send initialized notification
            self.send_notification("initialized", {})
            # Perform post-initialization
            self._post_initialize()

        return response or {"jsonrpc": "2.0", "id": params.get("id", 0), "result": {}}

    def _build_initialize_params(self, original_params: dict) -> dict:
        """Build AL-specific initialize params."""
        root_path = Path(self.root_path).resolve() if self.root_path else Path.cwd()
        root_uri = root_path.as_uri()

        return {
            "processId": os.getpid(),
            "rootPath": str(root_path),
            "rootUri": root_uri,
            "capabilities": {
                "workspace": {
                    "applyEdit": True,
                    "workspaceEdit": {
                        "documentChanges": True,
                        "resourceOperations": ["create", "rename", "delete"],
                        "failureHandling": "textOnlyTransactional",
                        "normalizesLineEndings": True,
                    },
                    "configuration": True,
                    "didChangeWatchedFiles": {"dynamicRegistration": True},
                    "symbol": {"dynamicRegistration": True, "symbolKind": {"valueSet": list(range(1, 27))}},
                    "executeCommand": {"dynamicRegistration": True},
                    "didChangeConfiguration": {"dynamicRegistration": True},
                    "workspaceFolders": True,
                },
                "textDocument": {
                    "synchronization": {
                        "dynamicRegistration": True,
                        "willSave": True,
                        "willSaveWaitUntil": True,
                        "didSave": True,
                    },
                    "completion": {
                        "dynamicRegistration": True,
                        "contextSupport": True,
                        "completionItem": {
                            "snippetSupport": True,
                            "commitCharactersSupport": True,
                            "documentationFormat": ["markdown", "plaintext"],
                            "deprecatedSupport": True,
                            "preselectSupport": True,
                        },
                    },
                    "hover": {"dynamicRegistration": True, "contentFormat": ["markdown", "plaintext"]},
                    "definition": {"dynamicRegistration": True, "linkSupport": True},
                    "references": {"dynamicRegistration": True},
                    "documentHighlight": {"dynamicRegistration": True},
                    "documentSymbol": {
                        "dynamicRegistration": True,
                        "symbolKind": {"valueSet": list(range(1, 27))},
                        "hierarchicalDocumentSymbolSupport": True,
                    },
                    "codeAction": {"dynamicRegistration": True},
                    "formatting": {"dynamicRegistration": True},
                    "rangeFormatting": {"dynamicRegistration": True},
                    "rename": {"dynamicRegistration": True, "prepareSupport": True},
                },
                "window": {
                    "showMessage": {"messageActionItem": {"additionalPropertiesSupport": True}},
                    "showDocument": {"support": True},
                    "workDoneProgress": True,
                },
            },
            "trace": "verbose",
            "workspaceFolders": [{"uri": root_uri, "name": root_path.name}],
        }

    def _post_initialize(self) -> None:
        """Perform AL-specific post-initialization."""
        if not self.root_path:
            return

        log("Post-initialization starting...")

        # Send workspace configuration
        self.send_notification(
            "workspace/didChangeConfiguration",
            {
                "settings": {
                    "workspacePath": self.root_path,
                    "alResourceConfigurationSettings": {
                        "assemblyProbingPaths": ["./.netpackages"],
                        "codeAnalyzers": [],
                        "enableCodeAnalysis": False,
                        "backgroundCodeAnalysis": "Project",
                        "packageCachePaths": ["./.alpackages"],
                        "ruleSetPath": None,
                        "enableCodeActions": True,
                        "incrementalBuild": False,
                        "outputAnalyzerStatistics": True,
                        "enableExternalRulesets": True,
                    },
                    "setActiveWorkspace": True,
                    "expectedProjectReferenceDefinitions": [],
                    "activeWorkspaceClosure": [self.root_path],
                }
            },
        )
        log("Sent workspace configuration")

        # Open app.json to trigger project loading
        app_json_path = Path(self.root_path) / "app.json"
        if app_json_path.exists():
            try:
                with open(app_json_path, encoding="utf-8") as f:
                    app_json_content = f.read()

                self.send_notification(
                    "textDocument/didOpen",
                    {
                        "textDocument": {
                            "uri": app_json_path.as_uri(),
                            "languageId": "json",
                            "version": 1,
                            "text": app_json_content,
                        }
                    },
                )
                log(f"Opened app.json: {app_json_path}")
            except Exception as e:
                log(f"Failed to open app.json: {e}")

        # Set active workspace
        try:
            workspace_uri = Path(self.root_path).resolve().as_uri()
            self.send_request(
                "al/setActiveWorkspace",
                {
                    "currentWorkspaceFolderPath": {
                        "uri": workspace_uri,
                        "name": Path(self.root_path).name,
                        "index": 0,
                    },
                    "settings": {
                        "workspacePath": self.root_path,
                        "setActiveWorkspace": True,
                    },
                },
            )
            log("Set active workspace")
        except Exception as e:
            log(f"Failed to set active workspace: {e}")

        # Wait for project to be fully loaded before returning
        self._wait_for_project_load(timeout=5)

        # Track this project as initialized
        if self.root_path:
            normalized_path = str(Path(self.root_path).resolve())
            self.initialized_projects.add(normalized_path)
            log(f"Tracked initial project: {normalized_path}")

        # Initialize call hierarchy server
        self._start_call_hierarchy_server()

        log("Post-initialization complete")

    def _start_call_hierarchy_server(self) -> None:
        """Start the al-call-hierarchy server for call hierarchy operations."""
        call_hierarchy_exe = find_call_hierarchy_executable()
        if not call_hierarchy_exe:
            log("al-call-hierarchy executable not found, call hierarchy disabled")
            return

        self.call_hierarchy_server = CallHierarchyServer()
        if not self.call_hierarchy_server.start(call_hierarchy_exe):
            log("Failed to start al-call-hierarchy server")
            self.call_hierarchy_server = None
            return

        # Initialize with workspace root (not AL project) to index all AL files
        # This handles workspaces with multiple AL projects
        workspace_path = self.workspace_root or self.root_path
        if workspace_path:
            workspace_uri = Path(workspace_path).as_uri()
            workspace_name = Path(workspace_path).name
            workspace_folders = [{"uri": workspace_uri, "name": workspace_name}]
            log(f"Initializing call hierarchy with workspace: {workspace_path}")
            if not self.call_hierarchy_server.initialize(workspace_uri, workspace_folders):
                log("Failed to initialize al-call-hierarchy server")
                self.call_hierarchy_server.shutdown()
                self.call_hierarchy_server = None
            else:
                log("al-call-hierarchy server ready")

    def check_project_loaded(self) -> bool:
        """Check if AL project is fully loaded using al/hasProjectClosureLoadedRequest."""
        try:
            response = self.send_request("al/hasProjectClosureLoadedRequest", {})
            log(f"Project load check response: {response}")
            if response and "result" in response:
                result = response["result"]
                log(f"Project load result: {result} (type: {type(result).__name__})")
                if isinstance(result, bool):
                    return result
                elif isinstance(result, dict):
                    return result.get("loaded", False)
                elif result is None:
                    # None might mean "not supported" - assume loaded
                    log("Project load returned None - assuming loaded")
                    return True
            return False
        except Exception as e:
            log(f"Project load check failed: {e}")
            return True  # Assume loaded if check fails

    def _wait_for_project_load(self, timeout: int = 5) -> bool:
        """Poll until project is loaded or timeout."""
        start = time.time()
        log(f"Waiting for project to load (timeout: {timeout}s)...")

        while time.time() - start < timeout:
            if self.check_project_loaded():
                elapsed = time.time() - start
                log(f"Project loaded after {elapsed:.1f}s")
                return True
            time.sleep(0.5)

        log(f"Project load check timed out after {timeout}s")
        return False

    def handle_definition(self, params: dict) -> dict:
        """Handle textDocument/definition using AL's custom command."""
        log("handle_definition called")
        uri = params["textDocument"]["uri"]
        log(f"Definition for URI: {uri}")

        # Ensure project is initialized for this file
        self._ensure_project_initialized(uri)

        # Ensure file is opened first
        self._ensure_file_opened(uri)

        # Try AL's custom gotodefinition first
        # AL LSP expects params wrapped in textDocumentPositionParams
        try:
            al_params = {
                "textDocumentPositionParams": {
                    "textDocument": params["textDocument"],
                    "position": params["position"],
                }
            }
            log(f"Sending al/gotodefinition with params: {al_params}")
            response = self.send_request("al/gotodefinition", al_params)
            if response and "result" in response:
                # Check if result is non-empty
                result = response.get("result")
                if not self._is_empty_definition_result(result):
                    return response

                # Result is empty - try documentSymbol fallback
                log("Definition result empty, trying documentSymbol fallback")
                fallback_location = self._try_document_symbol_fallback(params)
                if fallback_location:
                    return {"jsonrpc": "2.0", "result": fallback_location}

                # Return original empty response
                return response
        except Exception as e:
            log(f"al/gotodefinition failed: {e}")

        # Fallback to standard
        return self.send_request("textDocument/definition", params) or {}

    def _is_empty_definition_result(self, result) -> bool:
        """Check if a definition result is empty (null or empty array)."""
        if result is None:
            return True
        if isinstance(result, list) and len(result) == 0:
            return True
        return False

    def _try_document_symbol_fallback(self, params: dict):
        """Try to find symbol definition using hover + documentSymbol."""
        try:
            # Get symbol name via hover
            hover_params = {
                "textDocument": params["textDocument"],
                "position": params["position"],
            }
            hover_response = self.send_request("textDocument/hover", hover_params)
            if not hover_response or "result" not in hover_response:
                return None

            hover_result = hover_response.get("result")
            if not hover_result:
                return None

            symbol_name = self._extract_symbol_from_hover(hover_result)
            if not symbol_name:
                log("Could not extract symbol name from hover")
                return None

            log(f"Extracted symbol name from hover: {symbol_name}")

            # Get document symbols
            doc_symbol_params = {"textDocument": params["textDocument"]}
            symbols_response = self.send_request("textDocument/documentSymbol", doc_symbol_params)
            if not symbols_response or "result" not in symbols_response:
                return None

            symbols = symbols_response.get("result")
            if not symbols:
                return None

            # Find matching symbol
            location = self._find_symbol_location(symbols, symbol_name, params["textDocument"]["uri"])
            if location:
                log(f"Found symbol via documentSymbol fallback: {symbol_name}")
                return location

            return None
        except Exception as e:
            log(f"documentSymbol fallback failed: {e}")
            return None

    def _extract_symbol_from_hover(self, hover_result: dict) -> str:
        """Extract symbol name from hover response."""
        import re

        contents = hover_result.get("contents", {})
        if isinstance(contents, dict):
            content = contents.get("value", "")
        elif isinstance(contents, str):
            content = contents
        else:
            return ""

        if not content:
            return ""

        # AL hover typically returns markdown like:
        # "procedure TranslateEmailWithAI(...)" or
        # "local procedure Translate(...)"
        patterns = [
            # procedure Name or local procedure Name
            r'(?:local\s+)?procedure\s+("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)',
            # trigger OnRun or OnInsert etc
            r'trigger\s+("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)',
            # field "Name" or field Name
            r'field\s*\([^)]+\)\s+("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)',
            # var Name: Type - variable declarations
            r'var\s+("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)\s*:',
            # Generic: first identifier in the content (fallback)
            r'^[^A-Za-z_"]*("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)',
        ]

        for pattern in patterns:
            match = re.search(pattern, content)
            if match:
                name = match.group(1)
                # Remove quotes if present
                if name.startswith('"') and name.endswith('"'):
                    name = name[1:-1]
                return name

        return ""

    def _find_symbol_location(self, symbols: list, symbol_name: str, file_uri: str):
        """Search document symbols for a matching name and return its location."""
        for sym in symbols:
            name = sym.get("name", "")
            cleaned_name = self._clean_symbol_name(name)

            if name.lower() == symbol_name.lower() or cleaned_name.lower() == symbol_name.lower():
                # Check for Location (SymbolInformation format)
                if "location" in sym:
                    return sym["location"]
                # Check for range (DocumentSymbol format)
                if "selectionRange" in sym:
                    return {
                        "uri": file_uri,
                        "range": sym["selectionRange"]
                    }
                if "range" in sym:
                    return {
                        "uri": file_uri,
                        "range": sym["range"]
                    }

            # Search children (DocumentSymbol hierarchy)
            children = sym.get("children", [])
            if children:
                result = self._find_symbol_location(children, symbol_name, file_uri)
                if result:
                    return result

        return None

    def _clean_symbol_name(self, name: str) -> str:
        """Remove parameters and return type from symbol name."""
        idx = name.find("(")
        if idx > 0:
            return name[:idx].strip()
        return name

    def handle_hover(self, params: dict) -> dict:
        """Handle textDocument/hover with file opening."""
        log("handle_hover called")
        uri = params["textDocument"]["uri"]
        log(f"Hover for URI: {uri}")

        # Ensure project is initialized for this file
        self._ensure_project_initialized(uri)

        # Ensure file is opened first
        self._ensure_file_opened(uri)

        # Forward to AL LSP
        return self.send_request("textDocument/hover", params) or {}

    def handle_document_symbol(self, params: dict) -> dict:
        """Handle textDocument/documentSymbol with file opening."""
        log("handle_document_symbol called")
        uri = params["textDocument"]["uri"]
        log(f"Document symbol for URI: {uri}")

        # Ensure project is initialized for this file
        self._ensure_project_initialized(uri)

        # Ensure file is opened first
        self._ensure_file_opened(uri)

        # Now request symbols
        return self.send_request("textDocument/documentSymbol", params) or {}

    def handle_workspace_symbol(self, params: dict) -> dict:
        """Handle workspace/symbol with project initialization."""
        log("handle_workspace_symbol called")
        query = params.get("query", "")
        log(f"Workspace symbol query: '{query}'")

        # Return helpful error if query is empty
        # This is a known Claude Code LSP tool bug - it doesn't pass the query parameter
        # See: https://github.com/anthropics/claude-code/issues
        if not query or not query.strip():
            log("Empty query - returning error (Claude Code LSP bug)")
            return {
                "jsonrpc": "2.0",
                "error": {
                    "code": -32602,  # Invalid params
                    "message": "workspaceSymbol received empty query. This is a known Claude Code LSP tool bug - "
                               "the tool doesn't pass the 'query' parameter needed for symbol search. "
                               "WORKAROUND: Use 'documentSymbol' to list symbols in a specific file, "
                               "or use Grep to search for symbol names across the workspace."
                }
            }

        # Workaround: Claude Code passes file path as query instead of search term
        # Extract meaningful symbol name from AL file path
        if '\\' in query or '/' in query or query.endswith('.al'):
            original_query = query
            # Extract filename from path
            filename = query.replace('\\', '/').split('/')[-1]
            # Remove .al extension
            if filename.endswith('.al'):
                filename = filename[:-3]
            # AL files: "ObjectType ObjectId Name.al" - extract the Name part
            # e.g. "Table 6175301 CDO File" → "CDO File"
            parts = filename.split(' ', 2)  # Split into max 3 parts
            if len(parts) >= 3:
                query = parts[2]  # The name part
            else:
                query = filename
            log(f"Extracted query from file path: '{original_query}' → '{query}'")

        # Ensure the initial project is loaded
        if self.root_path:
            # Use a dummy file URI to trigger project initialization
            dummy_uri = Path(self.root_path).as_uri() + "/app.json"
            self._ensure_project_initialized(dummy_uri)

        # Wait for project to be fully loaded
        self._wait_for_project_load(timeout=3)

        # Try standard workspace/symbol first (use extracted query)
        search_params = {"query": query}
        response = self.send_request("workspace/symbol", search_params)
        result = response.get("result") if response else None
        if isinstance(result, list) and len(result) > 0:
            log(f"workspace/symbol returned {len(result)} results")
            return response

        # If no results, try AL-specific al/symbolSearch
        log("workspace/symbol returned no results, trying al/symbolSearch")
        al_response = self.send_request("al/symbolSearch", {"query": query})
        al_result = al_response.get("result") if al_response else None
        if isinstance(al_result, list) and len(al_result) > 0:
            log(f"al/symbolSearch returned {len(al_result)} results")
            return al_response

        # Ensure we always return an array (never null/None)
        log("No results from either workspace/symbol or al/symbolSearch")
        return {"jsonrpc": "2.0", "result": []}

    def handle_references(self, params: dict) -> dict:
        """Handle textDocument/references with file opening."""
        log("handle_references called")
        uri = params["textDocument"]["uri"]
        log(f"References for URI: {uri}")

        # Ensure project is initialized for this file
        self._ensure_project_initialized(uri)

        # Ensure file is opened first
        self._ensure_file_opened(uri)

        # Forward to AL LSP
        return self.send_request("textDocument/references", params) or {}

    def process_request(self, request: dict) -> dict:
        """Process incoming request and route appropriately."""
        method = request.get("method", "")
        params = request.get("params", {})
        request_id = request.get("id")

        log(f"Processing request: {method}")

        if method == "initialize":
            response = self.initialize(params)
            if response:
                response["id"] = request_id
            return response

        elif method == "textDocument/definition":
            response = self.handle_definition(params)
            if response:
                response["id"] = request_id
            return response

        elif method == "textDocument/documentSymbol":
            response = self.handle_document_symbol(params)
            if response:
                response["id"] = request_id
            return response

        elif method == "textDocument/hover":
            response = self.handle_hover(params)
            if response:
                response["id"] = request_id
            return response

        elif method == "workspace/symbol":
            response = self.handle_workspace_symbol(params)
            if response:
                response["id"] = request_id
            return response

        elif method == "textDocument/references":
            response = self.handle_references(params)
            if response:
                response["id"] = request_id
            return response

        elif method == "initialized":
            # initialized is a notification, not a request - no response expected
            self.send_notification(method, params)
            log("Forwarded initialized notification")
            return None  # No response for notifications

        elif method in (
            "textDocument/prepareCallHierarchy",
            "callHierarchy/incomingCalls",
            "callHierarchy/outgoingCalls",
        ):
            # Route to al-call-hierarchy server
            if self.call_hierarchy_server and self.call_hierarchy_server.initialized:
                log(f"Routing {method} to al-call-hierarchy")
                response = self.call_hierarchy_server.request(method, params)
                if response:
                    response["id"] = request_id
                    return response
                return {"jsonrpc": "2.0", "id": request_id, "result": None}
            else:
                log(f"Call hierarchy server not available for: {method}")
                return {
                    "jsonrpc": "2.0",
                    "id": request_id,
                    "error": {
                        "code": -32601,
                        "message": "Call hierarchy server not available",
                    },
                }

        else:
            # Pass through to AL LSP (for requests with id)
            if request_id is not None:
                response = self.send_request(method, params)
                if response:
                    response["id"] = request_id
                return response or {"jsonrpc": "2.0", "id": request_id, "result": None}
            else:
                # It's a notification, forward without expecting response
                self.send_notification(method, params)
                return None


def read_client_message() -> dict | None:
    """Read JSON-RPC message from stdin (Claude Code)."""
    try:
        headers = {}
        while True:
            line = sys.stdin.buffer.readline().decode("utf-8")
            if not line or line == "\r\n":
                break
            if ":" in line:
                key, value = line.split(":", 1)
                headers[key.strip()] = value.strip()

        if "Content-Length" not in headers:
            return None

        content_length = int(headers["Content-Length"])
        content = sys.stdin.buffer.read(content_length).decode("utf-8")
        return json.loads(content)
    except Exception as e:
        log(f"Error reading client message: {e}")
        return None


def write_client_message(msg: dict) -> None:
    """Write JSON-RPC message to stdout (Claude Code)."""
    content = json.dumps(msg)
    message = f"Content-Length: {len(content)}\r\n\r\n{content}"
    sys.stdout.buffer.write(message.encode("utf-8"))
    sys.stdout.buffer.flush()


def main():
    """Main entry point."""
    log("=" * 50)
    log("AL LSP Wrapper starting...")

    # Find AL extension
    extension_path = find_al_extension()
    if not extension_path:
        log("ERROR: AL extension not found")
        sys.exit(1)

    log(f"Found AL extension: {extension_path}")

    # Get executable
    executable = get_executable_path(extension_path)
    if not os.path.exists(executable):
        log(f"ERROR: Executable not found: {executable}")
        sys.exit(1)

    log(f"Using executable: {executable}")

    # Create wrapper and start AL LSP
    wrapper = ALLSPWrapper()
    wrapper.start(executable)

    # Main loop - proxy messages between Claude Code and AL LSP
    try:
        while True:
            # Read request from Claude Code
            request = read_client_message()
            if request is None:
                break

            log(f"Received from client: {request.get('method', request.get('id', 'unknown'))}")

            # Process and get response
            response = wrapper.process_request(request)

            # Send response to Claude Code
            if response:
                write_client_message(response)
                log(f"Sent to client: {response.get('id', 'notification')}")

    except KeyboardInterrupt:
        log("Interrupted")
    except Exception as e:
        log(f"Error in main loop: {e}")
    finally:
        if wrapper.call_hierarchy_server:
            wrapper.call_hierarchy_server.shutdown()
        if wrapper.process:
            wrapper.process.terminate()
        log("AL LSP Wrapper stopped")


if __name__ == "__main__":
    main()
