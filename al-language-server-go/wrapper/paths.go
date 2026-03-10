package wrapper

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// alExtensionVersion holds an extension path and its parsed version
type alExtensionVersion struct {
	path    string
	major   int
	minor   int
	patch   int
}

// vsCodeExtensionDirs lists all known VS Code variant extension directories (relative to home)
// Searched in priority order: stable VS Code first, then variants
var vsCodeExtensionDirs = []string{
	".vscode/extensions",                 // VS Code (stable)
	".vscode-insiders/extensions",        // VS Code Insiders
	".vscode-server/extensions",          // VS Code Server (Remote SSH, WSL, etc.)
	".vscode-server-insiders/extensions", // VS Code Server Insiders
	".vscode-oss/extensions",             // VSCodium
	".cursor/extensions",                 // Cursor
}

// FindALExtension locates the newest AL extension across all VS Code variant directories
func FindALExtension() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return findALExtensionInHome(home)
}

// findALExtensionInHome is the internal implementation that searches for AL extensions
// starting from the given home directory. Exported for testing.
func findALExtensionInHome(home string) (string, error) {
	// Find all AL extensions matching the pattern ms-dynamics-smb.al-*
	pattern := regexp.MustCompile(`^ms-dynamics-smb\.al-(\d+)\.(\d+)\.(\d+)$`)
	var alExtensions []alExtensionVersion
	var searchedDirs []string

	// Search all VS Code variant directories
	for _, relDir := range vsCodeExtensionDirs {
		extensionsDir := filepath.Join(home, relDir)
		searchedDirs = append(searchedDirs, extensionsDir)

		entries, err := os.ReadDir(extensionsDir)
		if err != nil {
			// Directory doesn't exist or can't be read, try next
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				matches := pattern.FindStringSubmatch(entry.Name())
				if matches != nil {
					major, _ := strconv.Atoi(matches[1])
					minor, _ := strconv.Atoi(matches[2])
					patch, _ := strconv.Atoi(matches[3])
					alExtensions = append(alExtensions, alExtensionVersion{
						path:  filepath.Join(extensionsDir, entry.Name()),
						major: major,
						minor: minor,
						patch: patch,
					})
				}
			}
		}
	}

	if len(alExtensions) == 0 {
		return "", fmt.Errorf("AL extension not found in any of: %s", strings.Join(searchedDirs, ", "))
	}

	// Sort by version (newest first) using proper semver comparison
	sort.Slice(alExtensions, func(i, j int) bool {
		if alExtensions[i].major != alExtensions[j].major {
			return alExtensions[i].major > alExtensions[j].major
		}
		if alExtensions[i].minor != alExtensions[j].minor {
			return alExtensions[i].minor > alExtensions[j].minor
		}
		return alExtensions[i].patch > alExtensions[j].patch
	})

	return alExtensions[0].path, nil
}

// GetALLSPExecutable returns the path to the AL Language Server executable
func GetALLSPExecutable(extensionPath string) string {
	var binDir, executable string

	switch runtime.GOOS {
	case "windows":
		binDir = "win32"
		executable = "Microsoft.Dynamics.Nav.EditorServices.Host.exe"
	case "linux":
		binDir = "linux"
		executable = "Microsoft.Dynamics.Nav.EditorServices.Host"
	case "darwin":
		binDir = "darwin"
		executable = "Microsoft.Dynamics.Nav.EditorServices.Host"
	default:
		binDir = "win32"
		executable = "Microsoft.Dynamics.Nav.EditorServices.Host.exe"
	}

	return filepath.Join(extensionPath, "bin", binDir, executable)
}

// FileURIToPath converts a file:// URI to a local file path
func FileURIToPath(uri string) (string, error) {
	if !strings.HasPrefix(uri, "file://") {
		return uri, nil // Return as-is if not a file URI
	}

	parsed, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("failed to parse URI: %w", err)
	}

	path := parsed.Path

	// On Windows, file URIs look like file:///C:/path
	// url.Parse gives us /C:/path, we need C:/path
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}

	// URL decode the path (handles %20 for spaces, etc.)
	decoded, err := url.PathUnescape(path)
	if err != nil {
		return "", fmt.Errorf("failed to decode path: %w", err)
	}

	return decoded, nil
}

// PathToFileURI converts a local file path to a file:// URI
func PathToFileURI(path string) string {
	// Normalize path separators
	path = filepath.ToSlash(path)

	// Escape special characters but NOT forward slashes
	// url.PathEscape escapes slashes which breaks file URIs
	var escaped strings.Builder
	for _, c := range path {
		switch {
		case c == '/':
			escaped.WriteRune('/')
		case c == ':':
			escaped.WriteRune(':')
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'):
			escaped.WriteRune(c)
		case c == '-' || c == '_' || c == '.' || c == '~':
			escaped.WriteRune(c)
		default:
			escaped.WriteString(url.PathEscape(string(c)))
		}
	}

	// On Windows, we need file:///C:/path
	if runtime.GOOS == "windows" && len(path) >= 2 && path[1] == ':' {
		return "file:///" + escaped.String()
	}

	// On Unix, we need file:///path
	return "file://" + escaped.String()
}

// NormalizePath returns a normalized absolute path
func NormalizePath(path string) string {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return filepath.Clean(absPath)
}

// GetLogPath returns the path for the log file (includes PID for multi-instance support)
func GetLogPath() string {
	return filepath.Join(GetLogDir(), fmt.Sprintf("al-lsp-wrapper-go-%d.log", os.Getpid()))
}

// GetLogDir returns the directory for log files
func GetLogDir() string {
	var tempDir string

	if runtime.GOOS == "windows" {
		tempDir = os.Getenv("TEMP")
		if tempDir == "" {
			tempDir = os.Getenv("TMP")
		}
		if tempDir == "" {
			tempDir = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local", "Temp")
		}
	} else {
		tempDir = "/tmp"
	}

	return tempDir
}

// GetLogPattern returns the glob pattern for finding log files
func GetLogPattern() string {
	return filepath.Join(GetLogDir(), "al-lsp-wrapper-go-*.log")
}

// ExtractSymbolFromPath extracts a symbol name from a file path
// This is a workaround for Claude Code sending file paths instead of symbol names
func ExtractSymbolFromPath(query string) string {
	// Check if it looks like a file path
	if strings.Contains(query, "/") || strings.Contains(query, "\\") {
		// Extract filename without extension
		base := filepath.Base(query)
		ext := filepath.Ext(base)
		name := strings.TrimSuffix(base, ext)

		// Remove common prefixes/patterns
		// e.g., "Tab18.Customer.dal" -> "Customer"
		parts := strings.Split(name, ".")
		if len(parts) > 1 {
			// Return the last meaningful part
			return parts[len(parts)-1]
		}
		return name
	}

	return query
}

// ResolveALExtensionPath resolves the AL extension path using this priority:
// 1. Explicit path from --al-extension-path flag (if non-empty)
// 2. AL_EXTENSION_PATH environment variable (if set)
// 3. Auto-discovery via FindALExtension()
func ResolveALExtensionPath(explicitPath string) (string, error) {
	if explicitPath != "" {
		return explicitPath, nil
	}

	if envPath := os.Getenv("AL_EXTENSION_PATH"); envPath != "" {
		return envPath, nil
	}

	return FindALExtension()
}

// IsALFile checks if a file is an AL file based on extension
func IsALFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".al" || ext == ".dal"
}
