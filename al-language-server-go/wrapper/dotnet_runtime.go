package wrapper

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// alHostBaseName / alMcpBaseName are extension-relative binary names without a
// platform extension; resolveExtensionBinary appends .exe on Windows.
const (
	alHostBaseName = "Microsoft.Dynamics.Nav.EditorServices.Host"
	alMcpBaseName  = "almcp"
)

// AL 18 went framework-dependent: the package ships portable IL plus a
// WINDOWS-ONLY apphost, and no Linux or macOS native launcher at all. So:
//
//   - Windows: the apphost starts only when a matching runtime is installed
//     machine-wide. The runtime the official AL extension acquires for itself
//     (via the ms-dotnettools.vscode-dotnet-runtime extension) lives in VS
//     Code globalStorage, which the apphost resolution never looks at — so a
//     machine can run the AL extension happily while our spawn dies with
//     "You must install .NET to run this application." (exit 131).
//   - Linux/macOS: there is nothing to spawn but the dll.
//
// This file resolves both: prefer the native apphost when it exists and will
// actually work, otherwise run `dotnet <dll>` with a runtime located here —
// which is what the official extension does.

// dotnetFramework is one shared-framework requirement from runtimeconfig.json.
type dotnetFramework struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type runtimeConfig struct {
	RuntimeOptions struct {
		TFM        string            `json:"tfm"`
		Framework  *dotnetFramework  `json:"framework"`
		Frameworks []dotnetFramework `json:"frameworks"`
	} `json:"runtimeOptions"`
}

// readRuntimeConfig returns the shared frameworks an app needs. A missing file
// means self-contained (or a pre-18 layout): nil, nil, and the caller keeps
// using the native binary exactly as before.
func readRuntimeConfig(path string) ([]dotnetFramework, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	var cfg runtimeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filepath.Base(path), err)
	}
	fws := cfg.RuntimeOptions.Frameworks
	// Single-framework apps use the singular key instead (alc does this).
	if cfg.RuntimeOptions.Framework != nil {
		fws = append(fws, *cfg.RuntimeOptions.Framework)
	}
	return fws, nil
}

// majorOf returns the leading major version number: "10.0.0" -> 10.
func majorOf(version string) int {
	head, _, _ := strings.Cut(version, ".")
	n, err := strconv.Atoi(head)
	if err != nil {
		return -1
	}
	return n
}

// rootSatisfies reports whether a .NET root provides every required framework
// at the required MAJOR version. Major-only on purpose: that is how roll-forward
// behaves by default (latestMinor within a major, never across majors), so a
// stricter check would reject runtimes that genuinely work.
func rootSatisfies(root string, required []dotnetFramework) bool {
	for _, fw := range required {
		major := majorOf(fw.Version)
		if major < 0 {
			return false
		}
		matches, _ := filepath.Glob(filepath.Join(root, "shared", fw.Name, fmt.Sprintf("%d.*", major)))
		if len(matches) == 0 {
			return false
		}
	}
	return true
}

// dotnetExe is the launcher inside a .NET root, or "" when absent.
func dotnetExe(root string) string {
	name := "dotnet"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(root, name)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// machineDotnetRoots lists roots a framework-dependent APPHOST can find on its
// own: DOTNET_ROOT, whatever `dotnet` on PATH belongs to, then the platform
// default install location. If one of these satisfies the app, the apphost
// will start and the launch should be left alone.
func machineDotnetRoots() []string {
	var roots []string
	add := func(p string) {
		if p == "" {
			return
		}
		for _, existing := range roots {
			if strings.EqualFold(existing, p) {
				return
			}
		}
		roots = append(roots, p)
	}

	add(os.Getenv("DOTNET_ROOT"))
	if lp, err := exec.LookPath("dotnet"); err == nil {
		if resolved, err := filepath.EvalSymlinks(lp); err == nil {
			lp = resolved
		}
		add(filepath.Dir(lp))
	}
	switch runtime.GOOS {
	case "windows":
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			add(filepath.Join(pf, "dotnet"))
		}
	case "darwin":
		add("/usr/local/share/dotnet")
	default:
		add("/usr/share/dotnet")
		add("/usr/lib/dotnet")
	}
	return roots
}

// vsCodeAcquiredDotnetRoots lists runtimes installed by the VS Code .NET
// Install Tool (dotnet.acquire), which is where the official AL extension gets
// its own runtime. These are invisible to the apphost, which is the entire
// reason this fallback exists. Layout:
//
//	<globalStorage>/ms-dotnettools.vscode-dotnet-runtime/.dotnet/<ver>~<arch>[~aspnetcore]/dotnet
//
// An "~aspnetcore" acquisition also carries Microsoft.NETCore.App, while a
// runtime-only one carries no ASP.NET — so rootSatisfies inspects the actual
// shared folders rather than trusting the directory name.
func vsCodeAcquiredDotnetRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	variants := []string{"Code", "Code - Insiders", "VSCodium", "Cursor"}
	var bases []string
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		for _, v := range variants {
			bases = append(bases, filepath.Join(appData, v, "User", "globalStorage"))
		}
	case "darwin":
		for _, v := range variants {
			bases = append(bases, filepath.Join(home, "Library", "Application Support", v, "User", "globalStorage"))
		}
	default:
		for _, v := range variants {
			bases = append(bases, filepath.Join(home, ".config", v, "User", "globalStorage"))
		}
	}
	// Remote/WSL sessions keep server-side extension data here instead.
	bases = append(bases,
		filepath.Join(home, ".vscode-server", "data", "User", "globalStorage"),
		filepath.Join(home, ".vscode-server-insiders", "data", "User", "globalStorage"),
	)

	var roots []string
	for _, base := range bases {
		matches, _ := filepath.Glob(filepath.Join(base, "ms-dotnettools.vscode-dotnet-runtime", ".dotnet", "*"))
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil && fi.IsDir() {
				roots = append(roots, m)
			}
		}
	}
	return roots
}

// nativeSuffix is the platform executable suffix for an apphost.
func nativeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// resolveExtensionBinDir picks the bin directory this app actually lives in.
//
// Deriving it from the native binary alone is wrong precisely where it matters
// most: on Linux/macOS with AL 18 there IS no native binary, so the native
// probe falls through to its legacy default and the dll beside it in the flat
// bin/ would never be found. Probe for ANY of the app files instead — native,
// dll, or runtimeconfig — flat first, then the legacy per-platform folder.
// Falls back to the legacy directory so "not found" errors read as before.
func resolveExtensionBinDir(extensionPath, baseName string) string {
	flat := filepath.Join(extensionPath, "bin")
	legacy := filepath.Join(flat, legacyBinDir())
	for _, dir := range []string{flat, legacy} {
		for _, suffix := range []string{nativeSuffix(), ".dll", ".runtimeconfig.json"} {
			if fileExists(filepath.Join(dir, baseName+suffix)) {
				return dir
			}
		}
	}
	return legacy
}

// launchResolver decides how to start an extension-bundled .NET app. The root
// sources are fields so tests supply fixtures instead of depending on whatever
// .NET the host machine happens to have.
type launchResolver struct {
	machineRoots func() []string
	extraRoots   func() []string
}

func (r launchResolver) machine() []string {
	if r.machineRoots == nil {
		return nil
	}
	return r.machineRoots()
}

func (r launchResolver) extra() []string {
	if r.extraRoots == nil {
		return nil
	}
	return r.extraRoots()
}

// resolve returns the command and args to launch baseName from an AL extension.
//
// Order, chosen so a setup that already works never changes:
//  1. Native apphost, when it exists AND either declares no framework need or a
//     machine-wide runtime satisfies it. That is every pre-18 install, and every
//     Windows box with .NET 10 installed properly.
//  2. `dotnet <dll>`, using any runtime found (machine-wide first, then VS
//     Code private acquisitions). Covers Windows-without-.NET-10 and all of
//     Linux/macOS on AL 18+.
//  3. The native apphost anyway when one exists — better to let its own
//     diagnostic reach the user than to invent one.
//  4. An error naming exactly what to install.
func (r launchResolver) resolve(extensionPath, baseName string) (string, []string, error) {
	binDir := resolveExtensionBinDir(extensionPath, baseName)
	native := filepath.Join(binDir, baseName+nativeSuffix())
	nativeExists := fileExists(native)

	dll := filepath.Join(binDir, baseName+".dll")
	required, err := readRuntimeConfig(filepath.Join(binDir, baseName+".runtimeconfig.json"))
	if err != nil {
		return "", nil, err
	}

	if nativeExists && len(required) == 0 {
		return native, nil, nil
	}
	if nativeExists {
		for _, root := range r.machine() {
			if rootSatisfies(root, required) {
				return native, nil, nil
			}
		}
	}

	if fileExists(dll) {
		candidates := append(append([]string{}, r.machine()...), r.extra()...)
		for _, root := range candidates {
			if !rootSatisfies(root, required) {
				continue
			}
			if exe := dotnetExe(root); exe != "" {
				return exe, []string{dll}, nil
			}
		}
	}

	if nativeExists {
		return native, nil, nil
	}
	return "", nil, fmt.Errorf(
		"cannot launch %s: this AL extension ships portable .NET code (no native binary for %s) "+
			"and no suitable .NET 10 runtime was found. Install the .NET 10 runtime, including "+
			"Microsoft.AspNetCore.App, from https://dotnet.microsoft.com/download/dotnet/10.0 "+
			"(needed: %s)",
		baseName, runtime.GOOS, describeFrameworks(required))
}

func describeFrameworks(fws []dotnetFramework) string {
	if len(fws) == 0 {
		return "a .NET runtime"
	}
	parts := make([]string, 0, len(fws))
	for _, f := range fws {
		parts = append(parts, f.Name+" "+f.Version)
	}
	return strings.Join(parts, ", ")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// defaultLaunchResolver is the production wiring.
func defaultLaunchResolver() launchResolver {
	return launchResolver{
		machineRoots: machineDotnetRoots,
		extraRoots:   vsCodeAcquiredDotnetRoots,
	}
}

// ResolveALHostLaunch returns the command+args to start the Microsoft AL
// Language Server for this extension.
func ResolveALHostLaunch(extensionPath string) (string, []string, error) {
	return defaultLaunchResolver().resolve(extensionPath, alHostBaseName)
}

// ResolveALMcpLaunch returns the command+args to start the bundled almcp.
func ResolveALMcpLaunch(extensionPath string) (string, []string, error) {
	return defaultLaunchResolver().resolve(extensionPath, alMcpBaseName)
}
