package poa

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// ---------------------------------------------------------------------------
// Tolerances
// ---------------------------------------------------------------------------

const (
	// moneyTol is half a penny — the right bar for a single line.
	moneyTol = 0.05
)

// ---------------------------------------------------------------------------
// Optional values
// ---------------------------------------------------------------------------

// Deref reads an optional float without panicking on nil.
// ok == false means the supplier does not print this field at all — which is
// not the same as printing zero, and must not be verified as though it were.
func Deref(p *float64) (float64, bool) {
	if p == nil {
		return 0, false
	}
	return *p, true
}

// Ptr returns the address of v. Needed because Go will not let you take the
// address of an expression or a function's return value. Each call allocates,
// so results never alias one another.
func Ptr[T any](v T) *T { return &v }

// rowHash is a stable identifier for a review row, derived from the fields
// that identify what it refers to (PF, BC line ID, POA line index). It lets
// a later feedback endpoint detect a row whose underlying line no longer
// exists (order re-run, line renumbered) without depending on description
// text, which can legitimately change between runs.
func rowHash(parts ...string) string {
	h := sha256.Sum256([]byte(fmt.Sprint(parts)))
	return hex.EncodeToString(h[:])[:16]
}
