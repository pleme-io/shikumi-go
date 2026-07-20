package shikumi

import "context"

// DiscoveryLayer is the missing discovered() tier seam (mirror the Rust
// DiscoveryLayer trait, discovered.rs:33-39) — one layer per kanchi axis-group.
// An undetectable axis returns an EMPTY map (a no-op in deepMerge → discovery
// totality by construction; mirror tiered.rs:526-534). This is DISTINCT from
// the existing file-path Discovery in discovery.go — that finds a config FILE;
// this contributes DISCOVERED-tier VALUES.
type DiscoveryLayer interface {
	// Name is a stable, human-facing label for provenance / debugging.
	Name() string
	// Discover returns the partial config this layer contributes, given the
	// current environment. An empty map ⇒ this axis is undetectable / has no
	// opinion → a no-op in the fold (never a panic, never an error).
	Discover(ctx context.Context) map[string]any
}

// DiscoveryLayerFunc adapts a name + function to a DiscoveryLayer.
type DiscoveryLayerFunc struct {
	N  string
	Fn func(context.Context) map[string]any
}

func (d DiscoveryLayerFunc) Name() string { return d.N }
func (d DiscoveryLayerFunc) Discover(ctx context.Context) map[string]any {
	if d.Fn == nil {
		return map[string]any{}
	}
	return d.Fn(ctx)
}

// DiscoveredFromLayers composes the Discovered tier's dict: bare is the floor,
// every layer's Discover() output deep-merges over it per leaf (mirror
// discovered_from_layers, tiered.rs:520-534). Over ZERO layers the result
// equals bare exactly; a layer returning an empty map is a no-op — discovery
// DEGRADES to bare, never fails (the kanchi _or_fallback totality guarantee,
// I2). bare is the caller's already-captured bare dict (a nested map[string]any
// clone so this never mutates the caller's copy).
func DiscoveredFromLayers(ctx context.Context, bare map[string]any, layers []DiscoveryLayer) map[string]any {
	merged := cloneDict(bare)
	for _, layer := range layers {
		if layer == nil {
			continue
		}
		contributed := layer.Discover(ctx)
		if len(contributed) == 0 {
			continue // undetectable axis: no-op, totality preserved
		}
		deepMerge(merged, contributed)
	}
	return merged
}

// cloneDict deep-copies a nested config map so a merge never mutates its input.
func cloneDict(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if sub, ok := asDict(v); ok {
			out[k] = cloneDict(sub)
		} else {
			out[k] = v
		}
	}
	return out
}
