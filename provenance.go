package shikumi

import (
	"sort"
	"strings"
)

// Provenance is the typed (tier, source) origin pair for an effective config
// leaf, mirroring the Rust Provenance (tiered.rs:576-580). Comparable struct —
// precedence ("which tier outranks which") is read from ConfigTierKind's const
// declaration order via TierOrdinal, never re-minted here.
type Provenance struct {
	Tier   ConfigTierKind
	Source ConfigSource
}

// NewProvenance constructs a provenance from an explicit (tier, source) pair
// (mirror Provenance::new, tiered.rs:582-587).
func NewProvenance(tier ConfigTierKind, source ConfigSource) Provenance {
	return Provenance{Tier: tier, Source: source}
}

// ComputedProvenance is a machine-derived tier (bare/discovered/prescribed):
// source is Defaults (mirror Provenance::computed, tiered.rs:592-598).
func ComputedProvenance(tier ConfigTierKind) Provenance {
	return Provenance{Tier: tier, Source: DefaultsSource()}
}

// FileProvenance is an operator FILE overlay — tier Custom, source File
// (mirror Provenance::file, tiered.rs:602-608).
func FileProvenance(path string) Provenance {
	return Provenance{Tier: TierCustom, Source: FileSource(path)}
}

// EnvProvenance is an operator ENV overlay — tier Custom, source Env with the
// given prefix (mirror Provenance::env, tiered.rs:610-618).
func EnvProvenance(prefix string) Provenance {
	return Provenance{Tier: TierCustom, Source: EnvSource(prefix)}
}

// TierOrdinal is the const-axis precedence ordinal of this provenance's tier —
// the fold's sort key (mirror Provenance::tier_ordinal, tiered.rs:637-639).
// A higher ordinal wins.
func (p Provenance) TierOrdinal() int { return p.Tier.Ordinal() }

// String renders the tier label plus the optional source detail through the
// typed ConfigSource — no free-form fmt of the whole provenance (mirror the
// Display impl, tiered.rs:642-654).
func (p Provenance) String() string {
	label := p.Tier.String()
	switch p.Source.Kind {
	case SourceEnv:
		return label + " (env: " + p.Source.Env + ")"
	case SourceFile:
		return label + " (file: " + p.Source.File + ")"
	default:
		return label
	}
}

// pathKeySep is a byte no config key can contain, used to join dotted-path
// COMPONENTS into a single map key so a segment containing '.' round-trips
// unambiguously — the Go equivalent of the Rust Vec<String> key (Go maps
// cannot key on a slice). See tiered.rs:661 (the Vec<String> guarantee).
const pathKeySep = "\x00"

// pathKey encodes dotted-path components into a single comparable map key.
func pathKey(path []string) string { return strings.Join(path, pathKeySep) }

// splitPathKey decodes a map key back into its components (inverse of pathKey).
// An empty key decodes to an empty slice (not a one-element {""} slice).
func splitPathKey(key string) []string {
	if key == "" {
		return nil
	}
	return strings.Split(key, pathKeySep)
}

// ProvenanceEntry is one (path, provenance) pair for deterministic dumps.
type ProvenanceEntry struct {
	Path       []string
	Provenance Provenance
}

// ProvenanceMap is the per-leaf provenance side-map for a progressively-
// resolved config (mirror tiered.rs:673-676). Keyed by dotted-path COMPONENTS
// joined with a NUL, so keys containing '.' round-trip. Constructed only by the
// fold (ResolveProgressive[With]).
type ProvenanceMap struct {
	inner map[string]Provenance
}

// newProvenanceMap allocates an empty attribution map (fold-internal).
func newProvenanceMap() *ProvenanceMap {
	return &ProvenanceMap{inner: map[string]Provenance{}}
}

// ProvenanceOf returns the provenance of the leaf named by path COMPONENTS, or
// (zero, false) if path names no attributed leaf (mirror provenance_of,
// tiered.rs:695-704). The variadic takes components — a component may itself
// contain '.' and is NOT split.
func (m ProvenanceMap) ProvenanceOf(path ...string) (Provenance, bool) {
	if m.inner == nil {
		return Provenance{}, false
	}
	p, ok := m.inner[pathKey(path)]
	return p, ok
}

// Len is the number of attributed leaves. Equal to the leaf count of the
// resolved config (bare() seeds every leaf) — mirror ProvenanceMap::len,
// tiered.rs:687-689.
func (m ProvenanceMap) Len() int { return len(m.inner) }

// IsEmpty reports whether no leaves are attributed.
func (m ProvenanceMap) IsEmpty() bool { return len(m.inner) == 0 }

// Entries returns the (path, provenance) pairs sorted lexicographically by
// path components, for deterministic dumps (the Go analog of the BTreeMap
// iteration order the Rust ProvenanceMap::entries exposes).
func (m ProvenanceMap) Entries() []ProvenanceEntry {
	keys := make([]string, 0, len(m.inner))
	for k := range m.inner {
		keys = append(keys, k)
	}
	// Sorting the NUL-joined keys is lexicographic by component, since the NUL
	// separator (0x00) sorts before any real key byte — the same ordering a
	// Rust BTreeMap<Vec<String>> produces.
	sort.Strings(keys)
	out := make([]ProvenanceEntry, 0, len(keys))
	for _, k := range keys {
		out = append(out, ProvenanceEntry{Path: splitPathKey(k), Provenance: m.inner[k]})
	}
	return out
}

// set records mark as the provenance of the leaf at path (fold-internal).
func (m *ProvenanceMap) set(path []string, mark Provenance) {
	m.inner[pathKey(path)] = mark
}

// purgePrefix drops every attributed leaf at or under path (fold-internal),
// mirroring the Rust attribution.retain(|p| !path_has_prefix(p, &path))
// (discovered.rs:4392).
func (m *ProvenanceMap) purgePrefix(path []string) {
	for k := range m.inner {
		if pathHasPrefix(splitPathKey(k), path) {
			delete(m.inner, k)
		}
	}
}
