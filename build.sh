#!/bin/bash
# Build script for AL Language Server wrappers
# Builds Go wrappers and fetches linked executables (al-call-hierarchy)
#
# Note: The Python wrapper is deprecated (v1.3.11+). The build script still
# copies al-call-hierarchy binaries to Python directories for backwards
# compatibility with existing installations.
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
    cp target/release/al-call-hierarchy.exe "$SCRIPT_DIR/al-language-server-python/bin/win32/" 2>/dev/null || \
    cp target/x86_64-pc-windows-msvc/release/al-call-hierarchy.exe "$SCRIPT_DIR/al-language-server-python/bin/win32/"
    echo "  -> Copied to al-language-server-go-windows/bin/ and al-language-server-python/bin/win32/"

    # Cross-compile for Linux (requires cross or appropriate toolchain)
    if command -v cross &> /dev/null; then
        echo "Building for Linux (using cross)..."
        cross build --release --target x86_64-unknown-linux-gnu
        cp target/x86_64-unknown-linux-gnu/release/al-call-hierarchy "$SCRIPT_DIR/al-language-server-go-linux/bin/"
        cp target/x86_64-unknown-linux-gnu/release/al-call-hierarchy "$SCRIPT_DIR/al-language-server-python/bin/linux/"
        echo "  -> Copied to al-language-server-go-linux/bin/ and al-language-server-python/bin/linux/"
    else
        echo "SKIP: Linux build (cross not installed)"
        echo "  Install with: cargo install cross"
    fi

    # Cross-compile for macOS (requires cross or appropriate toolchain)
    if command -v cross &> /dev/null; then
        echo "Building for macOS (using cross)..."
        cross build --release --target x86_64-apple-darwin
        cp target/x86_64-apple-darwin/release/al-call-hierarchy "$SCRIPT_DIR/al-language-server-go-darwin/bin/"
        cp target/x86_64-apple-darwin/release/al-call-hierarchy "$SCRIPT_DIR/al-language-server-python/bin/darwin/"
        echo "  -> Copied to al-language-server-go-darwin/bin/ and al-language-server-python/bin/darwin/"
    else
        echo "SKIP: macOS build (cross not installed)"
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

    echo "Building for macOS..."
    GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o ../al-language-server-go-darwin/bin/al-lsp-wrapper .
    echo "  -> al-language-server-go-darwin/bin/"
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

# Verify binaries exist and are executable
echo ""
echo "Verifying binaries..."
VERIFY_FAILED=false

if [ -f "$SCRIPT_DIR/al-language-server-go-windows/bin/al-call-hierarchy.exe" ]; then
    echo "  ✓ al-call-hierarchy.exe exists"
else
    echo "  ✗ al-call-hierarchy.exe missing"
    VERIFY_FAILED=true
fi

if [ -f "$SCRIPT_DIR/al-language-server-go-windows/bin/al-lsp-wrapper.exe" ]; then
    echo "  ✓ al-lsp-wrapper.exe exists"
else
    echo "  ✗ al-lsp-wrapper.exe missing"
    VERIFY_FAILED=true
fi

if [ "$VERIFY_FAILED" = true ]; then
    echo ""
    echo "ERROR: Some binaries are missing!"
    exit 1
fi

echo ""
echo "=== Build Summary ==="
echo "Windows binaries:"
ls -la "$SCRIPT_DIR/al-language-server-go-windows/bin/"
echo ""
echo "Linux binaries:"
ls -la "$SCRIPT_DIR/al-language-server-go-linux/bin/"
echo ""
echo "macOS binaries:"
ls -la "$SCRIPT_DIR/al-language-server-go-darwin/bin/"
echo ""
echo "=== Build complete ==="
