#!/bin/bash
# Build script for AL Language Server wrappers
# Builds Go wrappers and al-call-hierarchy (Rust)
#
# Usage: ./build.sh [--skip-go] [--skip-rust]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AL_CALL_HIERARCHY_DIR="$SCRIPT_DIR/../al-call-hierarchy"
TREE_SITTER_AL_DIR="$SCRIPT_DIR/../tree-sitter-al"

# Add common Go paths (for Git Bash on Windows)
export PATH="$PATH:/c/Program Files/Go/bin:/c/Go/bin:$HOME/go/bin"

SKIP_GO=false
SKIP_RUST=false

for arg in "$@"; do
    case $arg in
        --skip-go) SKIP_GO=true ;;
        --skip-rust) SKIP_RUST=true ;;
    esac
done

# Check if Docker Desktop is in Linux containers mode (required for cross-compilation)
check_docker_linux_mode() {
    if ! command -v docker &> /dev/null; then
        echo "  Docker not installed — skipping Linux cross-compilation"
        return 1
    fi
    local os_type
    os_type=$(docker info --format '{{.OSType}}' 2>/dev/null)
    if [ "$os_type" != "linux" ]; then
        echo "  ERROR: Docker Desktop is in Windows containers mode (OSType: ${os_type:-unknown})"
        echo "  Cross-compilation requires Linux containers."
        echo "  Right-click the Docker Desktop tray icon -> 'Switch to Linux containers...'"
        return 1
    fi
    return 0
}

echo "=== AL Language Server Wrapper Build Script ==="
echo ""

# Build al-call-hierarchy (Rust)
if [ "$SKIP_RUST" = false ]; then
    # Check if al-call-hierarchy repo exists
    if [ ! -d "$AL_CALL_HIERARCHY_DIR" ]; then
        echo "ERROR: al-call-hierarchy not found at $AL_CALL_HIERARCHY_DIR"
        echo "Please clone the al-call-hierarchy repository next to claude-code-lsps"
        exit 1
    fi

    # Check if tree-sitter-al repo exists (required for building)
    if [ ! -d "$TREE_SITTER_AL_DIR" ]; then
        echo "ERROR: tree-sitter-al not found at $TREE_SITTER_AL_DIR"
        echo "Please clone the tree-sitter-al repository next to claude-code-lsps"
        echo "  git clone https://github.com/AmpereComputing/tree-sitter-al ../tree-sitter-al"
        exit 1
    fi

    if [ ! -f "$TREE_SITTER_AL_DIR/src/parser.c" ]; then
        echo "ERROR: tree-sitter-al/src/parser.c not found"
        echo "The tree-sitter-al grammar may not be built. Try:"
        echo "  cd $TREE_SITTER_AL_DIR && tree-sitter generate"
        exit 1
    fi

    echo "=== Building al-call-hierarchy ==="
    echo "Using tree-sitter-al from: $TREE_SITTER_AL_DIR"
    cd "$AL_CALL_HIERARCHY_DIR"

    echo "Building for Windows..."
    cargo build --release --target x86_64-pc-windows-msvc 2>/dev/null || cargo build --release
    cp target/release/al-call-hierarchy.exe "$SCRIPT_DIR/al-language-server-go-windows/bin/" 2>/dev/null || \
    cp target/x86_64-pc-windows-msvc/release/al-call-hierarchy.exe "$SCRIPT_DIR/al-language-server-go-windows/bin/"
    echo "  -> Copied to al-language-server-go-windows/bin/"

    # Cross-compile for Linux (requires cross + Docker in Linux containers mode)
    if command -v cross &> /dev/null; then
        if check_docker_linux_mode; then
            # Mount tree-sitter-al into the Docker container and tell build.rs where to find it.
            # MSYS_NO_PATHCONV=1 prevents Git Bash from mangling /tree-sitter-al to C:/Program Files/Git/...
            export TREE_SITTER_AL_PATH="/tree-sitter-al"
            export CROSS_CONTAINER_OPTS="-v $TREE_SITTER_AL_DIR:/tree-sitter-al:ro"

            echo "Building for Linux (using cross)..."
            MSYS_NO_PATHCONV=1 cross build --release --target x86_64-unknown-linux-gnu
            cp target/x86_64-unknown-linux-gnu/release/al-call-hierarchy "$SCRIPT_DIR/al-language-server-go-linux/bin/"
            echo "  -> Copied to al-language-server-go-linux/bin/"
        fi
    else
        echo "SKIP: Linux Rust build (cross not installed)"
        echo "  Install with: cargo install cross"
    fi
else
    echo "=== Skipping al-call-hierarchy build (--skip-rust) ==="
fi

# Build Go wrappers
if [ "$SKIP_GO" = false ]; then
    echo ""
    echo "=== Building Go wrappers ==="

    if ! command -v go &> /dev/null; then
        echo "ERROR: Go not found in PATH"
        echo "Please install Go or add it to your PATH"
        exit 1
    fi

    cd "$SCRIPT_DIR/al-language-server-go"

    echo "Building for Windows..."
    go build -ldflags="-s -w" -o ../al-language-server-go-windows/bin/al-lsp-wrapper.exe .
    echo "  -> al-language-server-go-windows/bin/"

    echo "Building for Linux..."
    GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ../al-language-server-go-linux/bin/al-lsp-wrapper .
    echo "  -> al-language-server-go-linux/bin/"
else
    echo ""
    echo "=== Skipping Go wrapper build (--skip-go) ==="
fi

# Run tests to verify dependencies work
echo ""
echo "=== Running Unit Tests ==="

if [ "$SKIP_RUST" = false ]; then
    echo "Testing al-call-hierarchy..."
    cd "$AL_CALL_HIERARCHY_DIR"
    if cargo test 2>&1 | tail -5; then
        echo "  ✓ al-call-hierarchy tests passed"
    else
        echo "  ✗ al-call-hierarchy tests failed"
        exit 1
    fi
fi

# Run integration tests
echo ""
echo "=== Running Integration Tests ==="
cd "$SCRIPT_DIR/test-al-project"

if command -v python &> /dev/null; then
    echo "Testing al-call-hierarchy..."
    if python test_call_hierarchy.py 2>&1 | tail -15; then
        echo "  ✓ al-call-hierarchy tests passed"
    else
        echo "  ✗ al-call-hierarchy tests failed"
        exit 1
    fi

    echo ""
    echo "Testing Go wrapper integration..."
    if python test_lsp_go.py --wrapper go 2>&1 | tail -20; then
        echo "  ✓ Go wrapper integration tests passed"
    else
        echo "  ✗ Go wrapper integration tests failed"
        echo "  (This may be OK if AL Language Server is not installed)"
    fi
else
    echo "SKIP: Integration tests (python not found)"
fi

# Verify binaries exist
echo ""
echo "Verifying binaries..."
VERIFY_FAILED=false

for bin in \
    "$SCRIPT_DIR/al-language-server-go-windows/bin/al-call-hierarchy.exe" \
    "$SCRIPT_DIR/al-language-server-go-windows/bin/al-lsp-wrapper.exe" \
    "$SCRIPT_DIR/al-language-server-go-linux/bin/al-call-hierarchy" \
    "$SCRIPT_DIR/al-language-server-go-linux/bin/al-lsp-wrapper"; do
    if [ -f "$bin" ]; then
        echo "  ✓ $(basename "$bin") ($(dirname "$bin" | sed "s|$SCRIPT_DIR/||"))"
    else
        echo "  ✗ $(basename "$bin") MISSING ($(dirname "$bin" | sed "s|$SCRIPT_DIR/||"))"
        VERIFY_FAILED=true
    fi
done

if [ "$VERIFY_FAILED" = true ]; then
    echo ""
    echo "WARNING: Some binaries are missing!"
fi

echo ""
echo "=== Build Summary ==="
echo "Windows binaries:"
ls -la "$SCRIPT_DIR/al-language-server-go-windows/bin/"
echo ""
echo "Linux binaries:"
ls -la "$SCRIPT_DIR/al-language-server-go-linux/bin/"
echo ""
echo "=== Build complete ==="
