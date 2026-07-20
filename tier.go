package shikumi

import "slices"

// ConfigTierKind is the closed set of config resolution tiers, mirroring the
// Rust `ConfigTierKind` (shikumi/src/tiered.rs:146-158) — the Go analog of the
// Rust ClosedAxis. A small uint8 + iota keeps it comparable (usable as a map
// value / struct field) and cheap; the DECLARATION ORDER below IS the
// precedence order (higher wins), read structurally by Ordinal — never a
// hand-written parallel priority table.
//
//	bare → discovered[kanchi] → prescribed_default → file/env/runtime(Custom)
type ConfigTierKind uint8

const (
	// TierBare is the zero-opinion floor. Every field enumerated so the fold
	// seeds completeness (I5).
	TierBare ConfigTierKind = iota
	// TierDiscovered is bare() + runtime auto-detect (kanchi) outputs.
	TierDiscovered
	// TierDefault is bare() + discovered + prescribed_default() — the ~90% case.
	TierDefault
	// TierCustom is a file / env / runtime overlay on top of prescribed_default().
	// File/env/runtime overlays all land at this tier, differing only by their
	// ConfigSource.
	TierCustom
)

// configTierKindALL is the SINGLE ordered source of truth for precedence,
// mirroring `ConfigTierKind::ALL` (tiered.rs:170). Declaration order IS
// precedence: Ordinal reads position in this slice, so getting precedence
// wrong requires re-ordering this one slice (a source edit a test catches),
// never a hand-written per-tier priority.
var configTierKindALL = []ConfigTierKind{
	TierBare,
	TierDiscovered,
	TierDefault,
	TierCustom,
}

// Ordinal returns the const-axis precedence ordinal of the tier — its position
// in configTierKindALL (mirror axis_ordinal, cube.rs:373-381 / tier_ordinal,
// tiered.rs:637-639). Higher wins in the progressive fold. Precedence is
// DERIVED from the ALL slice, never hand-written. A tier not in the slice
// returns -1 (only reachable for a value forged outside the const set).
func (t ConfigTierKind) Ordinal() int {
	return slices.Index(configTierKindALL, t)
}

// String returns the canonical operator-facing lowercase tier name (mirror
// ConfigTierKind::as_str, tiered.rs:182-189). The single source of truth for
// the four tier names.
func (t ConfigTierKind) String() string {
	switch t {
	case TierBare:
		return "bare"
	case TierDiscovered:
		return "discovered"
	case TierDefault:
		return "default"
	case TierCustom:
		return "custom"
	default:
		return "unknown"
	}
}
