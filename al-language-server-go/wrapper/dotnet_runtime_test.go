package wrapper

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeDotnetRoot builds a directory shaped like a .NET install root: a dotnet
// executable plus shared/<framework>/<version> folders.
func fakeDotnetRoot(t *testing.T, frameworks map[string]string) string {
	t.Helper()
	root := t.TempDir()
	exe := "dotnet"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if err := os.WriteFile(filepath.Join(root, exe), nil, 0755); err != nil {
		t.Fatalf("write dotnet: %v", err)
	}
	for name, version := range frameworks {
		if err := os.MkdirAll(filepath.Join(root, "shared", name, version), 0755); err != nil {
			t.Fatalf("mkdir shared: %v", err)
		}
	}
	return root
}

// fakeExtension builds an AL extension dir. flat controls the bin layout;
// native says whether the platform apphost is present; rtcfg writes a
// framework-dependent runtimeconfig.json next to the dll.
func fakeExtension(t *testing.T, flat, native, dll, rtcfg bool) string {
	t.Helper()
	ext := t.TempDir()
	dir := filepath.Join(ext, "bin")
	if !flat {
		dir = filepath.Join(dir, legacyBinDir())
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	base := "Microsoft.Dynamics.Nav.EditorServices.Host"
	write := func(name string, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if native {
		n := base
		if runtime.GOOS == "windows" {
			n += ".exe"
		}
		write(n, "")
	}
	if dll {
		write(base+".dll", "")
	}
	if rtcfg {
		write(base+".runtimeconfig.json", `{"runtimeOptions":{"tfm":"net10.0","frameworks":[
			{"name":"Microsoft.NETCore.App","version":"10.0.0"},
			{"name":"Microsoft.AspNetCore.App","version":"10.0.0"}]}}`)
	}
	return ext
}

func TestResolveLaunch_PrefersNativeWhenTheRuntimeIsPresent(t *testing.T) {
	ext := fakeExtension(t, true, true, true, true)
	good := fakeDotnetRoot(t, map[string]string{
		"Microsoft.NETCore.App": "10.0.11", "Microsoft.AspNetCore.App": "10.0.11",
	})
	r := launchResolver{machineRoots: func() []string { return []string{good} }}

	cmd, args, err := r.resolve(ext, alHostBaseName)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(args) != 0 {
		t.Fatalf("expected the native apphost (no args), got %q %v", cmd, args)
	}
	if filepath.Ext(cmd) == ".dll" {
		t.Fatalf("expected native apphost, got dll launch: %q", cmd)
	}
}

func TestResolveLaunch_FallsBackToDotnetWhenNoMachineRuntime(t *testing.T) {
	// AL 18 on a box whose only .NET 10 is the one VS Code acquired privately:
	// the apphost cannot see it, so the wrapper must run the dll itself.
	ext := fakeExtension(t, true, true, true, true)
	acquired := fakeDotnetRoot(t, map[string]string{
		"Microsoft.NETCore.App": "10.0.11", "Microsoft.AspNetCore.App": "10.0.11",
	})
	r := launchResolver{
		machineRoots: func() []string {
			return []string{fakeDotnetRoot(t, map[string]string{"Microsoft.NETCore.App": "8.0.3"})}
		},
		extraRoots: func() []string { return []string{acquired} },
	}

	cmd, args, err := r.resolve(ext, alHostBaseName)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(args) != 1 || filepath.Ext(args[0]) != ".dll" {
		t.Fatalf("expected `dotnet <dll>`, got %q %v", cmd, args)
	}
	if filepath.Dir(cmd) != acquired {
		t.Fatalf("expected the acquired dotnet at %q, got %q", acquired, cmd)
	}
}

func TestResolveLaunch_NoNativeBinaryAtAllStillLaunchesViaDotnet(t *testing.T) {
	// AL 18 on Linux/macOS: the package ships NO native launcher, only IL.
	ext := fakeExtension(t, true, false, true, true)
	good := fakeDotnetRoot(t, map[string]string{
		"Microsoft.NETCore.App": "10.0.11", "Microsoft.AspNetCore.App": "10.0.11",
	})
	r := launchResolver{extraRoots: func() []string { return []string{good} }}

	cmd, args, err := r.resolve(ext, alHostBaseName)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(args) != 1 || filepath.Ext(args[0]) != ".dll" {
		t.Fatalf("expected `dotnet <dll>`, got %q %v", cmd, args)
	}
}

func TestResolveLaunch_LegacySelfContainedUsesNative(t *testing.T) {
	// AL 17.x: per-platform folder, native binary, no framework requirement.
	// Must behave exactly as before this change.
	ext := fakeExtension(t, false, true, false, false)
	r := launchResolver{machineRoots: func() []string { return nil }}

	cmd, args, err := r.resolve(ext, alHostBaseName)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(args) != 0 {
		t.Fatalf("expected native launch with no args, got %q %v", cmd, args)
	}
	if filepath.Base(filepath.Dir(cmd)) != legacyBinDir() {
		t.Fatalf("expected the legacy per-platform path, got %q", cmd)
	}
}

func TestResolveLaunch_ActionableErrorWhenNothingCanRun(t *testing.T) {
	// IL only, and no runtime anywhere: the user needs to be told what to install.
	ext := fakeExtension(t, true, false, true, true)
	r := launchResolver{}

	_, _, err := r.resolve(ext, alHostBaseName)
	if err == nil {
		t.Fatal("expected an error when neither a native binary nor a runtime exists")
	}
	for _, want := range []string{"NET 10", "Microsoft.AspNetCore.App"} {
		if !contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
