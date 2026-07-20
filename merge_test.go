package shikumi

import (
	"reflect"
	"testing"
)

// mark is a small helper: a Computed provenance for tier t.
func markT(t ConfigTierKind) Provenance { return ComputedProvenance(t) }

// TestDeepMergeAttributedSkipEqual: an incoming value equal to the base value
// is skipped wholesale in onChangeOnly mode — value AND attribution keep the
// LOWER tier's provenance (the last-changer rule, discovered.rs:4368-4370).
func TestDeepMergeAttributedSkipEqual(t *testing.T) {
	// Base starts EMPTY (as the real fold does); each layer merges in.
	base := map[string]any{}
	attr := newProvenanceMap()
	// First writer: Discovered sets port onto an empty base.
	deepMergeAttributed(base, map[string]any{"port": 8080}, nil, markT(TierDiscovered), attr, true)
	if got, _ := attr.ProvenanceOf("port"); got.Tier != TierDiscovered {
		t.Fatalf("first write: want Discovered, got %v", got.Tier)
	}
	// Second layer (Default) re-emits the SAME value → skipped → stays Discovered.
	deepMergeAttributed(base, map[string]any{"port": 8080}, nil, markT(TierDefault), attr, true)
	got, ok := attr.ProvenanceOf("port")
	if !ok || got.Tier != TierDiscovered {
		t.Fatalf("re-emit unchanged: want Discovered kept, got %v ok=%v", got.Tier, ok)
	}
	if base["port"] != 8080 {
		t.Fatalf("value changed on skip: %v", base["port"])
	}
}

// TestDeepMergeAttributedScalarReplace: a CHANGED scalar re-attributes to the
// higher tier and overwrites the value.
func TestDeepMergeAttributedScalarReplace(t *testing.T) {
	base := map[string]any{"port": 8080}
	attr := newProvenanceMap()
	deepMergeAttributed(base, map[string]any{"port": 8080}, nil, markT(TierBare), attr, true)
	deepMergeAttributed(base, map[string]any{"port": 9090}, nil, markT(TierCustom), attr, true)
	if base["port"] != 9090 {
		t.Fatalf("want 9090, got %v", base["port"])
	}
	if got, _ := attr.ProvenanceOf("port"); got.Tier != TierCustom {
		t.Fatalf("changed leaf: want Custom, got %v", got.Tier)
	}
}

// TestDeepMergeAttributedMapRecurse: same-key map+map recurses so SIBLING keys
// survive (only the changed child is re-attributed).
func TestDeepMergeAttributedMapRecurse(t *testing.T) {
	// Base starts empty; the Default tier merges in first (attributing both
	// leaves), then a Custom overlay changes only db.host.
	base := map[string]any{}
	attr := newProvenanceMap()
	deepMergeAttributed(base, map[string]any{
		"db": map[string]any{"host": "localhost", "port": 5432},
	}, nil, markT(TierDefault), attr, true)
	// Overlay changes only db.host.
	over := map[string]any{"db": map[string]any{"host": "prod"}}
	deepMergeAttributed(base, over, nil, markT(TierCustom), attr, true)

	db := base["db"].(map[string]any)
	if db["host"] != "prod" {
		t.Fatalf("db.host want prod, got %v", db["host"])
	}
	if db["port"] != 5432 {
		t.Fatalf("sibling db.port clobbered: %v", db["port"])
	}
	if got, _ := attr.ProvenanceOf("db", "host"); got.Tier != TierCustom {
		t.Fatalf("db.host want Custom, got %v", got.Tier)
	}
	if got, _ := attr.ProvenanceOf("db", "port"); got.Tier != TierDefault {
		t.Fatalf("db.port want Default (survived recurse), got %v", got.Tier)
	}
}

// TestDeepMergeAttributedArrayReplace: array-over-array REPLACES wholesale (NOT
// concatenated — figment Order::Merge parity, discovered.rs:41-68).
func TestDeepMergeAttributedArrayReplace(t *testing.T) {
	base := map[string]any{"hosts": []any{"a", "b"}}
	attr := newProvenanceMap()
	deepMergeAttributed(base, cloneDict(base), nil, markT(TierDefault), attr, true)
	deepMergeAttributed(base, map[string]any{"hosts": []any{"c"}}, nil, markT(TierCustom), attr, true)
	got := base["hosts"].([]any)
	if !reflect.DeepEqual(got, []any{"c"}) {
		t.Fatalf("array not replaced wholesale: %v", got)
	}
	if p, _ := attr.ProvenanceOf("hosts"); p.Tier != TierCustom {
		t.Fatalf("hosts want Custom, got %v", p.Tier)
	}
}

// TestDeepMergeAttributedDictOverScalarPurge: a dict overwriting a scalar (and
// vice versa) replaces AND purges prior sub-leaf attributions (pathHasPrefix
// retain, discovered.rs:4392).
func TestDeepMergeAttributedDictOverScalarPurge(t *testing.T) {
	// Start empty; merge "x" as a dict with two attributed leaves.
	base := map[string]any{}
	attr := newProvenanceMap()
	deepMergeAttributed(base, map[string]any{"x": map[string]any{"a": 1, "b": 2}}, nil, markT(TierDefault), attr, true)
	if attr.Len() != 2 {
		t.Fatalf("setup: want 2 leaves, got %d", attr.Len())
	}
	// Overlay replaces "x" with a scalar → x.a / x.b attributions must be purged.
	deepMergeAttributed(base, map[string]any{"x": 42}, nil, markT(TierCustom), attr, true)
	if _, ok := attr.ProvenanceOf("x", "a"); ok {
		t.Fatalf("x.a attribution not purged")
	}
	if _, ok := attr.ProvenanceOf("x", "b"); ok {
		t.Fatalf("x.b attribution not purged")
	}
	if p, ok := attr.ProvenanceOf("x"); !ok || p.Tier != TierCustom {
		t.Fatalf("x want Custom scalar, got %v ok=%v", p.Tier, ok)
	}
	if base["x"] != 42 {
		t.Fatalf("x value want 42, got %v", base["x"])
	}
}

// TestDeepMergeUnattributed: the unexported deepMerge twin — map recurse,
// scalar replace, array replace wholesale.
func TestDeepMergeUnattributed(t *testing.T) {
	base := map[string]any{
		"db":    map[string]any{"host": "localhost", "port": 5432},
		"hosts": []any{"a", "b"},
	}
	deepMerge(base, map[string]any{
		"db":    map[string]any{"host": "prod"},
		"hosts": []any{"c"},
	})
	db := base["db"].(map[string]any)
	if db["host"] != "prod" || db["port"] != 5432 {
		t.Fatalf("db recurse wrong: %v", db)
	}
	if !reflect.DeepEqual(base["hosts"], []any{"c"}) {
		t.Fatalf("array not replaced: %v", base["hosts"])
	}
}

// TestValuesEqualNumericNormalization: the change-aware skip treats int64 and
// float64 of the same magnitude as EQUAL, so "unchanged" means the same across
// tiers even after YAML/env decode representational splits (the sharpest parity
// risk).
func TestValuesEqualNumericNormalization(t *testing.T) {
	if !valuesEqual(int64(8080), float64(8080)) {
		t.Fatal("int64==float64 same magnitude should be equal")
	}
	if !valuesEqual(int(3), int64(3)) {
		t.Fatal("int==int64 should be equal")
	}
	if valuesEqual(int64(1), int64(2)) {
		t.Fatal("different ints must not be equal")
	}
	// bool and string are NOT numeric — must fall to DeepEqual, never coerce.
	if valuesEqual(true, int64(1)) {
		t.Fatal("bool must not numeric-coerce to int")
	}
	if !valuesEqual("x", "x") {
		t.Fatal("equal strings")
	}
	if valuesEqual("8080", int64(8080)) {
		t.Fatal("string must not numeric-coerce")
	}
}
