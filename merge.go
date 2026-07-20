package shikumi

import (
	"reflect"
	"slices"
)

// deepMergeAttributed is the ONE new algorithm — the attributed fold that
// mirrors the Rust deep_merge_attributed (discovered.rs:4354-4398)
// byte-for-byte in semantics. Per (key, incoming):
//
//   - change-aware skip: in on_change_only mode, an incoming value equal to the
//     value already at base[key] is skipped wholesale, so value AND attribution
//     stay put and the leaf keeps the LOWER tier's provenance
//     (discovered.rs:4368-4370). This is the last-changer rule (I6).
//   - map + map → recurse with the extended prefix so sibling keys keep their
//     prior attribution (discovered.rs:4377-4386).
//   - anything else → wholesale replace: purge any prior sub-leaf attributions
//     under this path, attribute the incoming value's leaves to mark, install
//     the value (discovered.rs:4392-4394).
//
// base/overlay are koanf-shaped nested maps (map[string]any); nested dicts are
// map[string]any, everything else (scalars AND arrays) is a leaf.
func deepMergeAttributed(
	base map[string]any,
	overlay map[string]any,
	prefix []string,
	mark Provenance,
	attribution *ProvenanceMap,
	onChangeOnly bool,
) {
	for key, incoming := range overlay {
		// Change-aware skip: an incoming value structurally equal to the one
		// already at this path leaves the prior attribution (and value) intact.
		// Only engaged in last-changer (onChangeOnly) mode; the last-writer
		// discovery-layer path never takes this branch.
		if onChangeOnly {
			if existing, ok := base[key]; ok && valuesEqual(existing, incoming) {
				continue
			}
		}
		path := make([]string, 0, len(prefix)+1)
		path = append(path, prefix...)
		path = append(path, key)

		baseVal, baseHas := base[key]
		baseMap, baseIsMap := asDict(baseVal)
		overMap, overIsMap := asDict(incoming)
		if baseHas && baseIsMap && overIsMap {
			// Two maps: recurse so sibling keys keep their prior attribution.
			deepMergeAttributed(baseMap, overMap, path, mark, attribution, onChangeOnly)
			// asDict may have normalized the base value's concrete type; keep the
			// (possibly-rebound) map so subsequent reads see the merged children.
			base[key] = baseMap
			continue
		}
		// Anything else: wholesale replace. Any prior sub-leaves under this path
		// are gone from the merged dict, so their attribution goes with them;
		// then re-attribute the new value's leaves before installing.
		attribution.purgePrefix(path)
		attributeLeaves(incoming, path, mark, attribution)
		base[key] = incoming
	}
}

// attributeLeaves records mark as the writer of every leaf reachable from path
// inside v (mirror attribute_leaves, discovered.rs:4408-4427). Recurses into
// map[string]any; every other value (scalars AND arrays) is a leaf attributed
// at its own path — matching figment, where arrays REPLACE and never merge.
func attributeLeaves(v any, path []string, mark Provenance, attribution *ProvenanceMap) {
	if inner, ok := asDict(v); ok {
		for k, sub := range inner {
			subPath := make([]string, 0, len(path)+1)
			subPath = append(subPath, path...)
			subPath = append(subPath, k)
			attributeLeaves(sub, subPath, mark, attribution)
		}
		return
	}
	attribution.set(path, mark)
}

// pathHasPrefix reports whether path names a descendant of (or equals) prefix
// in the dotted-path lattice (mirror path_has_prefix, discovered.rs:4402-4404).
func pathHasPrefix(path, prefix []string) bool {
	return len(path) >= len(prefix) && slices.Equal(path[:len(prefix)], prefix)
}

// deepMerge is the un-attributed twin used by DiscoveredFromLayers (mirror
// deep_merge, discovered.rs:41-68): same-key map+map recurses so sibling keys
// survive; scalars AND arrays replace wholesale (arrays are NOT concatenated —
// the figment Order::Merge parity). Kept UNEXPORTED so shikumi-go does NOT
// expose an unordered public merge primitive (Go ships slightly ahead of
// Rust's I3' only-mitigated `pub deep_merge` row).
func deepMerge(base, overlay map[string]any) {
	for key, incoming := range overlay {
		baseVal, baseHas := base[key]
		baseMap, baseIsMap := asDict(baseVal)
		overMap, overIsMap := asDict(incoming)
		if baseHas && baseIsMap && overIsMap {
			deepMerge(baseMap, overMap)
			base[key] = baseMap
			continue
		}
		base[key] = incoming
	}
}

// asDict returns (m, true) iff v is a nested config dict. koanf's confmap /
// Raw() yields map[string]any; a decoded YAML mapping is also map[string]any
// under the shared decoder. map[any]any (a legacy YAML shape) is normalized to
// map[string]any so the recursion is uniform.
func asDict(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			if ks, ok := k.(string); ok {
				out[ks] = val
			}
		}
		return out, true
	default:
		return nil, false
	}
}

// valuesEqual is the change-aware-skip equality predicate. It must mean the
// SAME "unchanged" across tiers as the decoder does, so the last-changer credit
// (I6) matches Rust's figment Value::PartialEq. Because every tier's raw dict is
// captured off the SAME koanf/confmap decode path (progressive.go), two leaves
// that decode to the same typed field carry the same concrete Go type here, so
// reflect.DeepEqual is exact for maps/slices/scalars. numericEqual bridges the
// one residual gap: YAML/env decode can yield int64 in one tier and float64 in
// another for the same field, which reflect.DeepEqual would call unequal — we
// normalize numerics to float64 before comparing so "unchanged" holds across
// that representational split.
func valuesEqual(a, b any) bool {
	if af, aok := numericFloat(a); aok {
		if bf, bok := numericFloat(b); bok {
			return af == bf
		}
		return false
	}
	return reflect.DeepEqual(a, b)
}

// numericFloat normalizes any Go numeric scalar to float64 (int/uint/float
// families), returning ok=false for non-numerics (incl. bool and string).
func numericFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}
