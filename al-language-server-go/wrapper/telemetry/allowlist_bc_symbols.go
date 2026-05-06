package telemetry

import "strings"

// allowedSegments is a tree of permitted namespace segments. A symbol like
// "Microsoft.Sales.Receivables" is allowed iff every segment is present at
// the corresponding tree level.
//
// Update when Microsoft ships new base-app namespaces. Source of truth is
// public AL base app code; do NOT add segments based on what looks like an
// "MS-y" name.
var allowedSegments = map[string]map[string]map[string]struct{}{
	"Microsoft": {
		"Sales": {
			"Receivables": {},
			"Document":    {},
		},
		"Foundation":  {},
		"Inventory":   {},
		"Purchases":   {},
		"Finance":     {},
	},
	"System": {
		"IO":        {},
		"Threading": {},
	},
}

// IsAllowedBCSymbol returns true when the dotted symbol path matches the
// allowlist tree at every segment. Empty input returns false.
func IsAllowedBCSymbol(s string) bool {
	if s == "" {
		return false
	}
	parts := strings.Split(s, ".")
	lvl2, ok := allowedSegments[parts[0]]
	if !ok {
		return false
	}
	if len(parts) == 1 {
		return true
	}
	lvl3, ok := lvl2[parts[1]]
	if !ok {
		return false
	}
	if len(parts) == 2 {
		return true
	}
	if _, ok := lvl3[parts[2]]; !ok {
		return false
	}
	if len(parts) == 3 {
		return true
	}
	// Depth beyond 3 levels rejected by design (spec: Allowlist source).
	return false
}
