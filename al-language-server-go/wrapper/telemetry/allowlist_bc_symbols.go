package telemetry

import (
	"fmt"
	"strings"
)

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

// ObjectIDRange classifies an AL object id by assigned range.
type ObjectIDRange int

const (
	RangeUnknown ObjectIDRange = iota
	RangeMSReserved
	RangeCustomer
	RangeMSTest
)

// ClassifyObjectID maps an AL object id to its range.
func ClassifyObjectID(id int) ObjectIDRange {
	switch {
	case id >= 1 && id <= 49999:
		return RangeMSReserved
	case id >= 50000 && id <= 99999:
		return RangeCustomer
	case id >= 130000 && id <= 150000:
		return RangeMSTest
	default:
		return RangeUnknown
	}
}

// CustomerBucket returns a coarse bucket label like "50xxx" for a customer-
// range id. Used to keep magnitude information without the exact id.
func CustomerBucket(id int) string {
	if id < 50000 || id > 99999 {
		return "non-customer"
	}
	base := (id / 1000) * 1000
	return fmt.Sprintf("%dxxx", base/1000)
}
