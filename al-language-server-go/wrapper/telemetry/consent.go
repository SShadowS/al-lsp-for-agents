package telemetry

// ConsentInputs are the values the wrapper resolves at startup.
type ConsentInputs struct {
	EnvVar      string // AL_LSP_TELEMETRY: off | errors | full
	CLIFlag     string // --telemetry: off | errors | full
	VSCodeLevel string // off | crash | error | all
	Launcher    string // vscode | claude-code | ""
}

// ConsentResult bundles the level and the reason it was chosen.
type ConsentResult struct {
	Level  ConsentLevel
	Reason string
}

// ResolveConsent computes the active level from the inputs. Highest-
// precedence non-empty input wins.
func ResolveConsent(in ConsentInputs) ConsentResult {
	if in.EnvVar != "" {
		return ConsentResult{Level: parseLevel(in.EnvVar), Reason: "env"}
	}
	if in.CLIFlag != "" {
		return ConsentResult{Level: parseLevel(in.CLIFlag), Reason: "flag"}
	}
	if in.VSCodeLevel != "" && in.Launcher == "vscode" {
		return ConsentResult{Level: vsCodeMap(in.VSCodeLevel), Reason: "vscode"}
	}
	return ConsentResult{Level: LevelErrors, Reason: "default"}
}

func parseLevel(s string) ConsentLevel {
	switch s {
	case "off":
		return LevelOff
	case "full":
		return LevelFull
	default:
		return LevelErrors
	}
}

func vsCodeMap(s string) ConsentLevel {
	switch s {
	case "off":
		return LevelOff
	case "all":
		return LevelFull
	default:
		return LevelErrors
	}
}

// errorsAutoOn lists the event names emitted at LevelErrors.
var errorsAutoOn = map[string]bool{
	"wrapper.panic":      true,
	"al_ls.failure":      true,
	"ms_bug.fingerprint": true,
	"download.failure":   true,
}

// EventAllowed returns true when the event is permitted at the active level.
func EventAllowed(name string, level ConsentLevel) bool {
	switch level {
	case LevelOff:
		return false
	case LevelFull:
		return true
	case LevelErrors:
		return errorsAutoOn[name]
	default:
		return false
	}
}
