package shikumi

import (
	"context"
	"fmt"
	"sort"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/maps"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/v2"
)

// ProgressiveLayer is the operator-overlay input to the fold (mirror
// ProgressiveLayer, tiered.rs:6093-6134): a partial config dict tagged with the
// Provenance to stamp on every leaf it wins. File/env/runtime overlays all land
// as Custom-tier layers, ordered AMONG THEMSELVES by append order (the stable
// sort keeps caller order within a tier).
type ProgressiveLayer struct {
	Provenance Provenance
	Dict       map[string]any
}

// NewProgressiveLayer constructs an overlay from an explicit provenance + dict
// (mirror ProgressiveLayer::new).
func NewProgressiveLayer(prov Provenance, dict map[string]any) ProgressiveLayer {
	return ProgressiveLayer{Provenance: prov, Dict: dict}
}

// FileLayer is an operator FILE overlay (mirror ProgressiveLayer::file).
func FileLayer(path string, dict map[string]any) ProgressiveLayer {
	return ProgressiveLayer{Provenance: FileProvenance(path), Dict: dict}
}

// EnvLayer is an operator ENV overlay (mirror ProgressiveLayer::env).
func EnvLayer(prefix string, dict map[string]any) ProgressiveLayer {
	return ProgressiveLayer{Provenance: EnvProvenance(prefix), Dict: dict}
}

// ProgressiveResolution is the ATOMIC (value, provenance) result of the fold
// (mirror ProgressiveResolution, tiered.rs:6145-6175). Constructed ONLY by
// ResolveProgressive[With], so a resolved value is never separable from
// "why is this X" (I4). Fields are unexported; access via Get/Prov/IntoParts.
type ProgressiveResolution[Root any] struct {
	value      Root
	provenance ProvenanceMap
}

// Get returns the resolved config value.
func (r ProgressiveResolution[Root]) Get() Root { return r.value }

// Prov returns the per-leaf provenance map.
func (r ProgressiveResolution[Root]) Prov() ProvenanceMap { return r.provenance }

// IntoParts yields both the value and its provenance map.
func (r ProgressiveResolution[Root]) IntoParts() (Root, ProvenanceMap) {
	return r.value, r.provenance
}

// ResolveProgressive is the idiomatic parity entry point: the sealed fold over
// the machine tiers with no operator overlays (mirror resolve_progressive,
// tiered.rs:438-440). Extends the existing Loader/Layer model — no foreign
// resolver graph.
func (l *Loader[Root]) ResolveProgressive(ctx context.Context) (ProgressiveResolution[Root], error) {
	return l.ResolveProgressiveWith(ctx, nil)
}

// ResolveProgressiveWith is THE fold, mirroring resolve_progressive_with
// (tiered.rs:457-503) exactly:
//
//  1. assemble the machine tiers (bare / discovered / prescribed) each as a
//     (Provenance, dict) tagged Computed(tier), capturing each tier's raw dict
//     off a PER-TIER fresh koanf so a tier's contribution is attributable;
//  2. append the operator overlays (file/env/runtime — Custom tier);
//  3. sort.SliceStable by prov.TierOrdinal() (mirror the sort_by_key at :481) so
//     no input ordering lets a lower tier beat a higher one, and same-tier
//     overlays keep append order;
//  4. fold via deepMergeAttributed(onChangeOnly=true) accumulating the
//     ProvenanceMap — the SOLE construction path for a resolved provenance;
//  5. decode Root from the merged dict via the shared decoder, keep-totality
//     fallback to prescribed on any decode error.
func (l *Loader[Root]) ResolveProgressiveWith(
	ctx context.Context,
	overlays []ProgressiveLayer,
) (ProgressiveResolution[Root], error) {
	// 1. Machine tiers as (Provenance, raw dict). Each tier's dict is captured
	//    off its OWN fresh koanf, so tier identity survives (the seam the
	//    single-shared-koanf runPipeline throws away).
	bareDict, err := l.tierDict(ctx, tierBare)
	if err != nil {
		return ProgressiveResolution[Root]{}, err
	}
	discoveredDict, err := l.tierDict(ctx, tierDiscovered)
	if err != nil {
		return ProgressiveResolution[Root]{}, err
	}
	prescribedDict, err := l.tierDict(ctx, tierPrescribed)
	if err != nil {
		return ProgressiveResolution[Root]{}, err
	}

	type taggedLayer struct {
		prov Provenance
		dict map[string]any
	}
	layers := []taggedLayer{
		{ComputedProvenance(TierBare), bareDict},
		{ComputedProvenance(TierDiscovered), discoveredDict},
		{ComputedProvenance(TierDefault), prescribedDict},
	}
	// 2. Operator overlays (already carry their own Custom-tier provenance).
	for _, ov := range overlays {
		layers = append(layers, taggedLayer{ov.Provenance, cloneDict(ov.Dict)})
	}

	// 3. Stable-sort by tier ordinal. A stable sort keeps same-tier overlays in
	//    caller (append) order.
	sort.SliceStable(layers, func(i, j int) bool {
		return layers[i].prov.TierOrdinal() < layers[j].prov.TierOrdinal()
	})

	// 4. Fold with per-leaf, change-aware attribution — the ONLY path that
	//    builds a ProgressiveResolution's provenance.
	merged := map[string]any{}
	attribution := newProvenanceMap()
	for _, layer := range layers {
		deepMergeAttributed(merged, layer.dict, nil, layer.prov, attribution, true)
	}

	// 5. Decode Root from the merged dict via the shared decoder. Every machine
	//    tier is a valid Root serialization; the fallback keeps totality.
	value, decodeErr := decodeDict[Root](merged)
	if decodeErr != nil {
		// Keep-totality fallback: prescribed_default (bareOr + tag defaults).
		value = l.prescribedValue(ctx)
	}
	return ProgressiveResolution[Root]{value: value, provenance: *attribution}, nil
}

// tierPhase names which machine tier a raw-dict capture targets.
type tierPhase uint8

const (
	tierBare tierPhase = iota
	tierDiscovered
	tierPrescribed
)

// tierDict runs the layers that define one machine tier onto a FRESH koanf and
// returns that tier's raw nested dict (lowercased-key namespace, matching the
// file/env layers). This is the per-tier raw-dict capture — the seam that lets
// the fold attribute a tier's contribution WITHOUT changing Load/runPipeline.
//
//   - bare        = the zero-opinion floor (Bare value if set, else zero Root).
//   - discovered  = bare + registered DiscoveryLayers (kanchi axes).
//   - prescribed  = the Defaults value + `default:` struct-tag defaults (the
//     opinionated first-launch config). This maps the existing single Defaults
//     onto the prescribed_default tier; bare stays the zero floor.
func (l *Loader[Root]) tierDict(ctx context.Context, phase tierPhase) (map[string]any, error) {
	switch phase {
	case tierBare:
		return l.bareDict(), nil
	case tierDiscovered:
		bare := l.bareDict()
		if len(l.discoveryLayers) == 0 {
			// Absent → the Discovered tier == bare (mirror the Rust default
			// discovered()=bare(), tiered.rs:350-352).
			return bare, nil
		}
		return DiscoveredFromLayers(ctx, bare, l.discoveryLayers), nil
	case tierPrescribed:
		return l.prescribedDict(ctx)
	default:
		return map[string]any{}, nil
	}
}

// bareDict is the zero-opinion floor as a nested dict: the Bare value if set,
// else the zero Root (mirror bare() as the enumerate-every-field floor that
// seeds completeness, I5).
func (l *Loader[Root]) bareDict() map[string]any {
	if l.hasBare {
		return nestedDict(l.bare)
	}
	var zero Root
	return nestedDict(zero)
}

// prescribedDict is the opinionated default tier as a nested dict. Mirroring
// the Rust convention that prescribed_default() is bare() + per-field overrides
// (tiered.rs:88), it is bare overlaid with ONLY the Defaults value's non-zero
// leaves (plus `default:` struct-tag defaults). This is the faithful mapping of
// shikumi-go's single partial Defaults onto the prescribed tier: a field the
// operator left at its Go zero does NOT clobber bare's floor (which would
// falsely claim it for the Default tier). The delta is computed by diffing the
// Defaults-with-tag-defaults dict against a pristine zero-Root-with-tag-defaults
// dict — only leaves the operator actually set survive.
func (l *Loader[Root]) prescribedDict(ctx context.Context) (map[string]any, error) {
	bare := l.bareDict()
	if !l.hasDefaults && !l.applyTagDefaults {
		// No opinions at all: prescribed == bare.
		return bare, nil
	}
	prescribedFull := nestedDict(l.prescribedValue(ctx))
	// The pristine baseline: a PLAIN zero Root (no tag defaults) as a nested
	// dict. Diffing prescribedFull against it isolates every leaf the operator's
	// Defaults value OR a `default:` tag set to a non-Go-zero value — those are
	// the prescribed tier's real opinions. A field left at its Go zero in both
	// Defaults and the tags is NOT an opinion and does not clobber bare's floor
	// (which would falsely claim it for Default). Tag-defaults ARE opinions and
	// survive (a tag default differs from the plain zero baseline).
	var zero Root
	baseline := nestedDict(zero)
	delta := dictDelta(baseline, prescribedFull)
	// Overlay the delta onto the bare floor: prescribed inherits bare, then the
	// operator's per-field overrides land on top.
	merged := cloneDict(bare)
	deepMerge(merged, delta)
	return merged, nil
}

// dictDelta returns the leaves of over that DIFFER from base (recursing into
// nested dicts). A leaf present in over but equal to base is dropped; a nested
// dict recurses and is kept only if it has a surviving child. This isolates the
// operator's real overrides from the zero/tag-default baseline.
func dictDelta(base, over map[string]any) map[string]any {
	out := map[string]any{}
	for k, ov := range over {
		bv, has := base[k]
		bm, bIsMap := asDict(bv)
		om, oIsMap := asDict(ov)
		if bIsMap && oIsMap {
			if sub := dictDelta(bm, om); len(sub) > 0 {
				out[k] = sub
			}
			continue
		}
		if !oIsMap {
			// Leaf: keep only if changed from base (or absent in base).
			if !has || !valuesEqual(bv, ov) {
				out[k] = ov
			}
			continue
		}
		// over is a dict, base is a scalar/absent: the whole subtree is a delta.
		if sub := dictDelta(map[string]any{}, om); len(sub) > 0 {
			out[k] = sub
		} else {
			out[k] = om
		}
	}
	return out
}

// prescribedValue materializes the prescribed_default Root: Defaults (or zero)
// with tag-defaults applied. The keep-totality fallback target.
func (l *Loader[Root]) prescribedValue(ctx context.Context) Root {
	var out Root
	if l.hasDefaults {
		out = l.defaults
	}
	if l.applyTagDefaults {
		_ = applyTagDefaults(ctx, &out)
	}
	return out
}

// nestedDict decodes a typed Root into a lowercased-key nested map[string]any
// (the same namespace the file/env layers use), via the shared struct tag. This
// is the tier→dict encoder the fold merges over; it mirrors what
// tiered_to_dict does on the Rust side.
func nestedDict(v any) map[string]any {
	nested := map[string]any{}
	dc := &mapstructure.DecoderConfig{Result: &nested, TagName: structTag}
	dec, err := mapstructure.NewDecoder(dc)
	if err != nil {
		return map[string]any{}
	}
	if err := dec.Decode(v); err != nil {
		return map[string]any{}
	}
	return lowerKeys(nested)
}

// decodeDict materializes a Root from a merged nested dict using the SAME koanf
// + shared decoder path Load uses, so decode semantics never drift between the
// fold and the classic loader. Flattening first keeps confmap's dotted-key
// contract; the shared decoderConfig applies WeaklyTypedInput + the
// TextUnmarshaller hook exactly as runPipeline does.
func decodeDict[Root any](merged map[string]any) (Root, error) {
	var out Root
	flat, _ := maps.Flatten(merged, nil, ".")
	k := koanf.New(".")
	if err := k.Load(confmap.Provider(flat, "."), nil); err != nil {
		return out, fmt.Errorf("shikumi: progressive load: %w", err)
	}
	if err := k.UnmarshalWithConf("", &out, koanf.UnmarshalConf{
		Tag:           structTag,
		DecoderConfig: decoderConfig(&out),
	}); err != nil {
		return out, fmt.Errorf("shikumi: progressive decode: %w", err)
	}
	return out, nil
}
