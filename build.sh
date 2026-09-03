#!/bin/bash
# Build script for AL Language Server wrappers
# Builds Go wrappers and al-call-hierarchy (Rust)
#
# Usage: ./build.sh [--skip-go] [--skip-rust] [--replace-engine]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AL_CALL_HIERARCHY_DIR="$SCRIPT_DIR/../al-call-hierarchy"
TREE_SITTER_AL_DIR="$SCRIPT_DIR/../tree-sitter-al"

# Add common Go paths (for Git Bash on Windows)
export PATH="$PATH:/c/Program Files/Go/bin:/c/Go/bin:$HOME/go/bin"

SKIP_GO=false
SKIP_RUST=false
# al-call-hierarchy in the PLUGIN dirs is owned by al-sem's build-and-deploy
# workflow: it commits CI-built, Authenticode-SIGNED binaries straight into this
# repo, and those are what marketplace users download. A local build is neither
# signed nor (on Linux, which drops the telemetry feature below) the same binary,
# so copying one over the deployed artifact silently downgrades what ships.
# During the v1.14.0 release this happened and was caught only by a manual
# signature check. Default is therefore to leave the deployed engine alone;
# pass --replace-engine when you deliberately want your local engine build in
# the plugin dirs (e.g. testing an al-sem change before it is released).
REPLACE_ENGINE=false

for arg in "$@"; do
    case $arg in
        --skip-go) SKIP_GO=true ;;
        --skip-rust) SKIP_RUST=true ;;
        --replace-engine) REPLACE_ENGINE=true ;;
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
        echo "Please clone the al-call-hierarchy repository next to al-lsp-for-agents"
        exit 1
    fi

    # Check if tree-sitter-al repo exists (required for building)
    if [ ! -d "$TREE_SITTER_AL_DIR" ]; then
        echo "ERROR: tree-sitter-al not found at $TREE_SITTER_AL_DIR"
        echo "Please clone the tree-sitter-al repository next to al-lsp-for-agents"
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
    # Copy whichever artifact the build above actually wrote. Both target dirs can
    # exist at once (an ad-hoc plain `cargo build --release` leaves target/release/
    # behind), and a fixed preference order silently ships the STALE one — that
    # exact miss shipped a pre-version-bump alsem once. Newest mtime wins.
    # `alsem` is the CLI half of the same cargo workspace, and `cargo build`
    # already produces it. Review agents call it over Bash (`alsem analyze <app>
    # --format pr-summary`), which is why it travels with the plugin: the
    # entrypoint git-pulls this repo on every container start, so a new binary
    # here reaches every container without an image rebuild. It is always ours
    # to ship — the deploy bot does not copy it. The ENGINE is only ours with
    # --replace-engine; see REPLACE_ENGINE above.
    win_binaries="alsem.exe"
    if [ "$REPLACE_ENGINE" = true ]; then
        win_binaries="al-call-hierarchy.exe alsem.exe"
    else
        echo "  NOTE: leaving the deployed al-call-hierarchy.exe alone (--replace-engine to override)"
    fi
    for b in $win_binaries; do
        newest=$(ls -t "target/x86_64-pc-windows-msvc/release/$b" "target/release/$b" 2>/dev/null | head -1)
        if [ -z "$newest" ]; then
            echo "  ERROR: no built $b found in either target dir" >&2
            exit 1
        fi
        cp "$newest" "$SCRIPT_DIR/al-language-server-go-windows/bin/"
    done
    # The dev/test harness binary is always refreshed: test_lsp_go.py runs it,
    # and a stale copy there means the suite silently tests old code.
    dev_engine=$(ls -t target/x86_64-pc-windows-msvc/release/al-call-hierarchy.exe target/release/al-call-hierarchy.exe 2>/dev/null | head -1)
    cp "$dev_engine" "$SCRIPT_DIR/al-language-server-go/bin/al-call-hierarchy.exe"
    echo "  -> Copied to al-language-server-go-windows/bin/ and al-language-server-go/bin/"

    # Cross-compile for Linux (requires cross + Docker in Linux containers mode)
    if command -v cross &> /dev/null; then
        if check_docker_linux_mode; then
            # Mount tree-sitter-al into the Docker container and tell build.rs where to find it.
            # MSYS_NO_PATHCONV=1 prevents Git Bash from mangling /tree-sitter-al to C:/Program Files/Git/...
            export TREE_SITTER_AL_PATH="/tree-sitter-al"
            # `-e CARGO_BUILD_RUSTC_WRAPPER=` blanks the rustc wrapper inside the
            # container. A host ~/.cargo/config.toml setting `rustc-wrapper = "sccache"`
            # is read by cargo in there too, but sccache is not installed in the cross
            # image, so every build dies with
            #   error: could not execute process `sccache rustc -vV` (never executed)
            export CROSS_CONTAINER_OPTS="-v $TREE_SITTER_AL_DIR:/tree-sitter-al:ro -e CARGO_BUILD_RUSTC_WRAPPER="

            echo "Building for Linux (using cross)..."
            # --no-default-features drops the `telemetry` feature, which is default-on and
            # pulls opentelemetry-application-insights -> reqwest -> openssl-sys. The cross
            # image has no libssl-dev, so with telemetry on the build dies in openssl-sys
            # ("Could not find directory of OpenSSL installation"). Dropping it is also what
            # we want in the container on its own merits: these binaries run over
            # customer-adjacent source, and this build has no network stack at all.
            # The Windows build above keeps default features — it is a local dev binary.
            if ! MSYS_NO_PATHCONV=1 cross build --release --target x86_64-unknown-linux-gnu --no-default-features; then
                # Without this the script exits 0 with the Linux binaries silently
                # untouched, and stale ones ship to every container on the next pull.
                echo "  ERROR: Linux cross-compilation FAILED — Linux binaries NOT updated" >&2
                exit 1
            fi
            lin_binaries="alsem"
            if [ "$REPLACE_ENGINE" = true ]; then
                lin_binaries="al-call-hierarchy alsem"
            else
                echo "  NOTE: leaving the deployed al-call-hierarchy alone (--replace-engine to override)"
            fi
            for b in $lin_binaries; do
                cp "target/x86_64-unknown-linux-gnu/release/$b" "$SCRIPT_DIR/al-language-server-go-linux/bin/"
                chmod +x "$SCRIPT_DIR/al-language-server-go-linux/bin/$b"
                # The +x bit has to be recorded in the index too: the container consumes
                # this repo via `git pull`, so a binary that is executable only in this
                # working tree arrives unexecutable everywhere else.
                git -C "$SCRIPT_DIR" update-index --chmod=+x "al-language-server-go-linux/bin/$b" 2>/dev/null || true
            done
            echo "  -> Copied to al-language-server-go-linux/bin/ (with +x)"
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
    go build -trimpath -ldflags="-s -w" -o ../al-language-server-go-windows/bin/al-lsp-wrapper.exe .
    echo "  -> al-language-server-go-windows/bin/"

    echo "Building for Linux..."
    GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o ../al-language-server-go-linux/bin/al-lsp-wrapper .
    chmod +x ../al-language-server-go-linux/bin/al-lsp-wrapper
    git -C "$SCRIPT_DIR" update-index --chmod=+x al-language-server-go-linux/bin/al-lsp-wrapper
    echo "  -> al-language-server-go-linux/bin/ (with +x)"
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

# Verify Linux binaries have execute permission in git (prevents EACCES on Linux)
echo ""
echo "Verifying Linux binary permissions in git..."
for bin in al-language-server-go-linux/bin/al-call-hierarchy al-language-server-go-linux/bin/al-lsp-wrapper; do
    mode=$(git -C "$SCRIPT_DIR" ls-files -s "$bin" 2>/dev/null | awk '{print $1}')
    if [ "$mode" = "100755" ]; then
        echo "  ✓ $(basename "$bin") has execute permission"
    elif [ "$mode" = "100644" ]; then
        echo "  ✗ $(basename "$bin") missing execute permission — fixing..."
        git -C "$SCRIPT_DIR" update-index --chmod=+x "$bin"
        echo "    Fixed. Remember to commit this change."
    elif [ -z "$mode" ]; then
        echo "  - $(basename "$bin") not tracked by git (skip)"
    fi
done

echo ""
echo "=== Build Summary ==="
echo "Windows binaries:"
ls -la "$SCRIPT_DIR/al-language-server-go-windows/bin/"
echo ""
echo "Linux binaries:"
ls -la "$SCRIPT_DIR/al-language-server-go-linux/bin/"
echo ""
echo "=== Build complete ==="
