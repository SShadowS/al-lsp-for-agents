package telemetry

// perfThresholds is the per-method outlier threshold in milliseconds.
// Methods not in the table use defaultThresholdMs. Adding/changing entries
// requires updating the test.
var perfThresholds = map[string]int{
	"al/gotodefinition":            5000,
	"textDocument/hover":           2000,
	"textDocument/documentSymbol":  5000,
	"textDocument/completion":      3000,
	"textDocument/references":      5000,
	"callHierarchy/incomingCalls":  10000,
	"callHierarchy/outgoingCalls":  10000,
}

const defaultThresholdMs = 10000

// PerfThresholdMs returns the outlier threshold for an LSP method.
func PerfThresholdMs(method string) int {
	if v, ok := perfThresholds[method]; ok {
		return v
	}
	return defaultThresholdMs
}
