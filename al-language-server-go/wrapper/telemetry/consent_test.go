package telemetry

import "testing"

func TestConsentDefault(t *testing.T) {
	in := ConsentInputs{}
	if got := ResolveConsent(in); got.Level != LevelErrors {
		t.Errorf("default = %v, want errors", got.Level)
	}
	if got := ResolveConsent(in); got.Reason != "default" {
		t.Errorf("reason = %q", got.Reason)
	}
}

func TestConsentEnvWins(t *testing.T) {
	in := ConsentInputs{EnvVar: "off", CLIFlag: "full", VSCodeLevel: "all", Launcher: "vscode"}
	if got := ResolveConsent(in); got.Level != LevelOff {
		t.Errorf("env should win; got %v", got.Level)
	}
}

func TestConsentCLIFlagOverridesVSCode(t *testing.T) {
	in := ConsentInputs{CLIFlag: "off", VSCodeLevel: "all", Launcher: "vscode"}
	if got := ResolveConsent(in); got.Level != LevelOff {
		t.Errorf("CLI flag should beat VS Code; got %v", got.Level)
	}
}

func TestConsentVSCodeLevelOnlyTrustedWhenLauncherIsVSCode(t *testing.T) {
	in := ConsentInputs{VSCodeLevel: "off", Launcher: "claude-code"}
	if got := ResolveConsent(in); got.Level != LevelErrors {
		t.Errorf("VS Code level must not affect non-vscode launcher; got %v", got.Level)
	}
}

func TestConsentVSCodeMapping(t *testing.T) {
	cases := []struct {
		vsLevel string
		want    ConsentLevel
	}{
		{"off", LevelOff},
		{"crash", LevelErrors},
		{"error", LevelErrors},
		{"all", LevelFull},
	}
	for _, c := range cases {
		got := ResolveConsent(ConsentInputs{VSCodeLevel: c.vsLevel, Launcher: "vscode"})
		if got.Level != c.want {
			t.Errorf("vscode %q -> %v, want %v", c.vsLevel, got.Level, c.want)
		}
	}
}

func TestEventAllowedAtLevel(t *testing.T) {
	cases := []struct {
		name  string
		level ConsentLevel
		want  bool
	}{
		{"wrapper.panic", LevelErrors, true},
		{"al_ls.failure", LevelErrors, true},
		{"ms_bug.fingerprint", LevelErrors, true},
		{"download.failure", LevelErrors, true},
		{"lsp.request_error", LevelErrors, false},
		{"lsp.capability_gap", LevelErrors, false},
		{"perf.outlier", LevelErrors, false},
		{"config.error", LevelErrors, false},
		{"lsp.request_error", LevelFull, true},
		{"perf.outlier", LevelFull, true},
		{"wrapper.panic", LevelOff, false},
	}
	for _, c := range cases {
		if got := EventAllowed(c.name, c.level); got != c.want {
			t.Errorf("EventAllowed(%q, %v) = %v, want %v", c.name, c.level, got, c.want)
		}
	}
}
