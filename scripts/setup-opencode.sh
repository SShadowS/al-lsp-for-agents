#!/usr/bin/env bash
set -euo pipefail

# AL LSP for Agents — OpenCode Setup Script
# Downloads AL LSP binaries and configures OpenCode to use them.
# Usage:
#   Interactive:  ./setup-opencode.sh
#   One-liner:    curl -fsSL https://raw.githubusercontent.com/SShadowS/al-lsp-for-agents/main/scripts/setup-opencode.sh | bash
#   Project scope: curl -fsSL ... | SCOPE=project bash

REPO="SShadowS/al-lsp-for-agents"
INSTALL_BASE="$HOME/.local/share/al-lsp"
INSTALL_DIR="$INSTALL_BASE/bin"
VERSION_FILE="$INSTALL_BASE/.version"
GLOBAL_CONFIG="$HOME/.config/opencode/opencode.json"
PROJECT_CONFIG="./opencode.json"

# Colors (disabled if not a terminal)
if [ -t 1 ]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[0;33m'
    NC='\033[0m'
else
    RED=''
    GREEN=''
    YELLOW=''
    NC=''
fi

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }
fatal() { error "$@"; exit 1; }

check_dependencies() {
    local missing=()
    for cmd in curl jq tar; do
        if ! command -v "$cmd" &>/dev/null; then
            missing+=("$cmd")
        fi
    done
    if [ ${#missing[@]} -gt 0 ]; then
        fatal "Missing required commands: ${missing[*]}. Please install them and try again."
    fi
}

check_al_extension() {
    local search_dirs=(
        "$HOME/.vscode/extensions"
        "$HOME/.vscode-insiders/extensions"
        "$HOME/.vscode-server/extensions"
        "$HOME/.vscode-server-insiders/extensions"
        "$HOME/.vscode-oss/extensions"
        "$HOME/.cursor/extensions"
    )

    # Collect ALL matches across all directories, then pick the newest
    local all_matches=()
    for dir in "${search_dirs[@]}"; do
        if [ -d "$dir" ]; then
            while IFS= read -r ext_dir; do
                if [ -n "$ext_dir" ]; then
                    all_matches+=("$ext_dir")
                fi
            done < <(find "$dir" -maxdepth 1 -type d -name "ms-dynamics-smb.al-*" 2>/dev/null)
        fi
    done

    if [ ${#all_matches[@]} -eq 0 ]; then
        fatal "Microsoft AL Language extension not found.
Install it in VS Code:  ext install ms-dynamics-smb.al
Or specify a custom path via --al-extension-path in the OpenCode config after setup.
Searched: ${search_dirs[*]}"
    fi

    # Sort by version number (numeric semver comparison, matches paths.go logic)
    # Extract version from directory name, sort by major.minor.patch numerically
    local latest
    latest=$(printf '%s\n' "${all_matches[@]}" | \
        sed 's/.*ms-dynamics-smb\.al-//' | \
        sort -t. -k1,1nr -k2,2nr -k3,3nr | \
        head -1)
    # Reconstruct full path: find the match that ends with this version
    latest=$(printf '%s\n' "${all_matches[@]}" | grep "ms-dynamics-smb.al-${latest}$" | head -1)

    info "Found AL extension: $latest"
}

download_and_extract() {
    info "Fetching latest release info..."
    local release_json
    release_json=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest") \
        || fatal "Failed to fetch release info from GitHub. Check your internet connection."

    local tag
    tag=$(echo "$release_json" | jq -r '.tag_name')

    if [ -z "$tag" ] || [ "$tag" = "null" ]; then
        fatal "Could not determine latest release tag."
    fi

    # Check if already up to date
    if [ -f "$VERSION_FILE" ] && [ "$(cat "$VERSION_FILE")" = "$tag" ]; then
        info "Already up to date ($tag). Skipping download."
        return 0
    fi

    local asset_name="al-lsp-wrapper-linux-x64.tar.gz"
    local download_url
    download_url=$(echo "$release_json" | jq -r --arg name "$asset_name" \
        '.assets[] | select(.name == $name) | .browser_download_url')

    if [ -z "$download_url" ] || [ "$download_url" = "null" ]; then
        fatal "Release $tag does not contain asset '$asset_name'."
    fi

    info "Downloading $tag ($asset_name)..."
    local tmpdir
    tmpdir=$(mktemp -d)
    trap 'rm -rf "$tmpdir"' EXIT

    curl -fsSL -o "$tmpdir/$asset_name" "$download_url" \
        || fatal "Failed to download $download_url"

    info "Extracting to $INSTALL_DIR..."
    mkdir -p "$INSTALL_DIR"
    # Archive contains flat files (no subdirectory): al-lsp-wrapper, al-call-hierarchy
    tar xzf "$tmpdir/$asset_name" -C "$INSTALL_DIR" \
        || fatal "Failed to extract archive."

    find "$INSTALL_DIR" -maxdepth 1 -type f -exec chmod +x {} +

    # Write version file
    mkdir -p "$INSTALL_BASE"
    echo "$tag" > "$VERSION_FILE"

    info "Installed $tag to $INSTALL_DIR"
}

determine_config_path() {
    # Non-interactive (piped): use SCOPE env var, default to global
    if [ ! -t 0 ]; then
        if [ "${SCOPE:-}" = "project" ]; then
            echo "$PROJECT_CONFIG"
        else
            echo "$GLOBAL_CONFIG"
        fi
        return
    fi

    # Interactive: ask the user (prompts go to stderr so stdout stays clean for return value)
    echo "" >&2
    echo "Where should the OpenCode config be written?" >&2
    echo "  1) Global  ($GLOBAL_CONFIG)" >&2
    echo "  2) Project (./opencode.json)" >&2
    echo "" >&2
    read -rp "Choice [1]: " choice
    case "${choice:-1}" in
        2) echo "$PROJECT_CONFIG" ;;
        *) echo "$GLOBAL_CONFIG" ;;
    esac
}

write_config() {
    local config_path="$1"
    local wrapper_path="$INSTALL_DIR/al-lsp-wrapper"

    # Build the AL config object using jq for proper JSON escaping
    local al_config
    al_config=$(jq -n --arg cmd "$wrapper_path" \
        '{"command": [$cmd], "extensions": [".al", ".dal"]}')

    if [ -f "$config_path" ]; then
        # Merge into existing config — ensure .lsp exists before setting .lsp.al
        local tmpfile
        tmpfile=$(mktemp)
        if jq --argjson al "$al_config" '.lsp //= {} | .lsp.al = $al' "$config_path" > "$tmpfile"; then
            mv "$tmpfile" "$config_path"
            info "Updated $config_path"
        else
            rm -f "$tmpfile"
            fatal "Failed to merge config into $config_path. Is the file valid JSON?"
        fi
    else
        # Write new config file
        mkdir -p "$(dirname "$config_path")"
        jq -n --argjson al "$al_config" '{"lsp": {"al": $al}}' > "$config_path"
        info "Created $config_path"
    fi
}

main() {
    info "AL LSP for Agents — OpenCode Setup"
    echo ""

    check_dependencies
    check_al_extension
    download_and_extract

    local config_path
    config_path=$(determine_config_path)
    write_config "$config_path"

    echo ""
    info "Setup complete!"
    info "Binary location: $INSTALL_DIR"
    info "Config written to: $config_path"
    echo ""
    info "Open an .al file in OpenCode to verify the language server starts."
}

main "$@"
