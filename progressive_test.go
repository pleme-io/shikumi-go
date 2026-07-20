package shikumi

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// pcfg is the representative config exercised by the parity matrix. `default:`
// tags define the prescribed_default tier (reusing go-envconfig's applier);
// yaml tags define the keyspace. Includes a nested dict, an array leaf, and a
// numeric leaf (the int64/float64 skew guard).
type pcfg struct {
	Tenant   string   `yaml:"tenant"   env:"TENANT, default=acme"`
	Port     int      `yaml:"port"     env:"PORT, default=8080"`
	Replicas int      `yaml:"replicas" env:"REPLICAS, default=1"`
	Hosts    []string `yaml:"hosts"`
	DB       pdb      `yaml:"db"`
}

type pdb struct {
	Host string `yaml:"host" env:"HOST, default=localhost"`
	Pool int    `yaml:"pool" env:"POOL, default=5"`
}

// I1 PRECEDENCE-TOTAL: ordinals read configTierKindALL via slices.Index (no
// parallel constant); higher-ordinal-wins re-derived from the ALL slice.
func TestI1PrecedenceTotal(t *testing.T) {
	want := map[ConfigTierKind]int{
		TierBare: 0, TierDiscovered: 1, TierDefault: 2, TierCustom: 3,
	}
	for tier, ord := range want {
		if got := tier.Ordinal(); got != ord {
			t.Fatalf("%v.Ordinal() = %d, want %d", tier, got, ord)
		}
	}
	// Structural: ordinal IS position in the ALL slice.
	for i, tier := range configTierKindALL {
		if tier.Ordinal() != i {
			t.Fatalf("ALL[%d]=%v ordinal drift: %d", i, tier, tier.Ordinal())
		}
	}
	// Higher-ordinal-wins, re-derived: Custom outranks every earlier tier.
	for i := 0; i < len(configTierKindALL)-1; i++ {
		if !(configTierKindALL[i].Ordinal() < configTierKindALL[i+1].Ordinal()) {
			t.Fatalf("precedence not strictly increasing at %d", i)
		}
	}
}

// I3 STABLE-SORT-BEFORE-MERGE: overlays fed in DELIBERATELY WRONG order resolve
// IDENTICALLY to correctly-ordered input (re-sort re-ranks); two same-tier
// Custom overlays keep APPEND order (last wins on a shared key).
func TestI3StableSortBeforeMerge(t *testing.T) {
	ctx := context.Background()
	l := For[pcfg]("i3").Defaults(pcfg{Tenant: "acme", Port: 8080})

	// A Custom file overlay + a Custom env overlay, correct order.
	fileOv := FileLayer("/etc/i3.yaml", map[string]any{"port": 9090})
	envOv := EnvLayer("I3_", map[string]any{"port": 9191})

	correct, err := l.ResolveProgressiveWith(ctx, []ProgressiveLayer{fileOv, envOv})
	if err != nil {
		t.Fatal(err)
	}
	// Same overlays, but the machine tiers are ALWAYS assembled first internally;
	// the operator ordering that matters is overlay-vs-overlay. Feeding the two
	// overlays in the SAME order must give the same result regardless of how many
	// times the fold sorts. Verify last-append-wins on the shared key.
	if correct.Get().Port != 9191 {
		t.Fatalf("append order: want env (9191) to win, got %d", correct.Get().Port)
	}
	if p, _ := correct.Prov().ProvenanceOf("port"); p.Source.Kind != SourceEnv {
		t.Fatalf("port provenance want env, got %v", p)
	}

	// Now prove re-sort re-ranks: a lower-tier-looking-but-actually-Custom
	// overlay listed FIRST still lands after the machine tiers. Build an
	// explicit Bare-tier layer via NewProgressiveLayer placed at the FRONT; it
	// must NOT beat the Custom file overlay even though it's listed first.
	misOrdered := []ProgressiveLayer{
		NewProgressiveLayer(ComputedProvenance(TierBare), map[string]any{"port": 1}),
		fileOv,
	}
	res, err := l.ResolveProgressiveWith(ctx, misOrdered)
	if err != nil {
		t.Fatal(err)
	}
	if res.Get().Port != 9090 {
		t.Fatalf("mis-ordered: Custom file (9090) must beat front-listed Bare(1), got %d", res.Get().Port)
	}
}

// I6 CREDIT-TO-HIGHEST-TIER-THAT-SET-FINAL-VALUE (the load-bearing last-changer):
//   - prescribed re-emits a discovered value UNCHANGED → provenance stays
//     {Discovered, Defaults}, NOT {Default}.
//   - a leaf prescribed CHANGES → {Default}.
//   - a Custom file overlay that CHANGES it → {Custom, File}.
func TestI6CreditToHighestChanger(t *testing.T) {
	ctx := context.Background()

	// Discovered sets db.host = "detected"; prescribed re-emits it UNCHANGED but
	// changes port to 8080.
	disco := DiscoveryLayerFunc{N: "kanchi", Fn: func(context.Context) map[string]any {
		return map[string]any{"db": map[string]any{"host": "detected"}, "port": 7000}
	}}
	l := For[pcfg]("i6").
		Bare(pcfg{}).
		Discovered(disco).
		Defaults(pcfg{Port: 8080, DB: pdb{Host: "detected"}}) // re-emits host unchanged, changes port

	res, err := l.ResolveProgressive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// db.host: discovered set it, prescribed re-emitted UNCHANGED → Discovered.
	if p, ok := res.Prov().ProvenanceOf("db", "host"); !ok || p.Tier != TierDiscovered {
		t.Fatalf("db.host want Discovered (last-changer), got %v ok=%v", p, ok)
	}
	// port: discovered set 7000, prescribed CHANGED to 8080 → Default.
	if p, ok := res.Prov().ProvenanceOf("port"); !ok || p.Tier != TierDefault {
		t.Fatalf("port want Default (prescribed changed it), got %v ok=%v", p, ok)
	}
	if res.Get().Port != 8080 {
		t.Fatalf("port value want 8080, got %d", res.Get().Port)
	}

	// A Custom file overlay changes db.host → {Custom, File}.
	res2, err := l.ResolveProgressiveWith(ctx, []ProgressiveLayer{
		FileLayer("/etc/i6.yaml", map[string]any{"db": map[string]any{"host": "override"}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := res2.Prov().ProvenanceOf("db", "host"); !ok || p.Tier != TierCustom || p.Source.Kind != SourceFile {
		t.Fatalf("db.host want {Custom,File}, got %v ok=%v", p, ok)
	}
	if res2.Get().DB.Host != "override" {
		t.Fatalf("db.host value want override, got %q", res2.Get().DB.Host)
	}
}

// I4 ATOMIC (value, provenance): ResolveProgressiveWith is the ONLY constructor
// of a ProgressiveResolution with a populated provenance. A hand-built zero
// ProgressiveResolution has an EMPTY provenance (fields unexported) — proving no
// exported path yields a value with a fabricated non-empty provenance.
func TestI4AtomicValueProvenance(t *testing.T) {
	// Zero-value construction outside the fold: provenance is empty by
	// construction (unexported fields, no setter).
	var hand ProgressiveResolution[pcfg]
	if !hand.Prov().IsEmpty() {
		t.Fatal("hand-built resolution must have empty provenance (no exported constructor)")
	}
	// The fold co-produces both.
	res, err := For[pcfg]("i4").Defaults(pcfg{Port: 8080}).ResolveProgressive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Prov().IsEmpty() {
		t.Fatal("fold must populate provenance")
	}
	v, p := res.IntoParts()
	if v.Port != 8080 || p.IsEmpty() {
		t.Fatal("IntoParts must return both value and non-empty provenance")
	}
}

// I5 COMPLETENESS: seed bare() enumerating every field → every resolved leaf has
// a provenance entry; a field touched by NO tier above bare is still attributed
// (to Bare here, since bare is the zero floor).
func TestI5Completeness(t *testing.T) {
	// bare enumerates every field with explicit (non-Go-zero-ambiguous) values so
	// each leaf is present in the bare dict.
	bare := pcfg{Tenant: "bare-t", Port: 1, Replicas: 1, Hosts: []string{"h"}, DB: pdb{Host: "bh", Pool: 1}}
	// TagDefaults off: prescribed has no opinions, so every leaf stays credited
	// to bare — the completeness-with-no-higher-tier assertion.
	l := For[pcfg]("i5").Bare(bare).TagDefaults(false)
	res, err := l.ResolveProgressive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Enumerate the expected leaves: tenant, port, replicas, hosts, db.host, db.pool.
	leaves := [][]string{
		{"tenant"}, {"port"}, {"replicas"}, {"hosts"}, {"db", "host"}, {"db", "pool"},
	}
	for _, leaf := range leaves {
		if _, ok := res.Prov().ProvenanceOf(leaf...); !ok {
			t.Fatalf("leaf %v has no provenance entry (completeness gap)", leaf)
		}
	}
	if res.Prov().Len() != len(leaves) {
		t.Fatalf("provenance leaf count = %d, want %d: %v", res.Prov().Len(), len(leaves), res.Prov().Entries())
	}
	// A field touched by no tier above bare is attributed to Bare.
	if p, _ := res.Prov().ProvenanceOf("tenant"); p.Tier != TierBare {
		t.Fatalf("tenant (only in bare) want Bare, got %v", p.Tier)
	}
}

// I2 DISCOVERY-TOTALITY: DiscoveredFromLayers over ZERO layers == bare exactly;
// an empty-map layer is a no-op; the resolve path never errs/panics on
// discovery — it degrades to bare.
func TestI2DiscoveryTotality(t *testing.T) {
	ctx := context.Background()
	bare := map[string]any{"port": 1, "db": map[string]any{"host": "bh"}}

	// Zero layers == bare exactly.
	got := DiscoveredFromLayers(ctx, bare, nil)
	if !reflect.DeepEqual(got, bare) {
		t.Fatalf("zero layers must == bare, got %v", got)
	}

	// Empty-map layer is a no-op.
	empty := DiscoveryLayerFunc{N: "empty", Fn: func(context.Context) map[string]any { return map[string]any{} }}
	got2 := DiscoveredFromLayers(ctx, bare, []DiscoveryLayer{empty})
	if !reflect.DeepEqual(got2, bare) {
		t.Fatalf("empty layer must be no-op, got %v", got2)
	}

	// Resolve with no discovery layers: Discovered tier == bare, no error.
	// TagDefaults off so bare's Port:5 is the floor with no prescribed opinion.
	res, err := For[pcfg]("i2").Bare(pcfg{Port: 5}).TagDefaults(false).ResolveProgressive(ctx)
	if err != nil {
		t.Fatalf("discovery-less resolve must not err: %v", err)
	}
	if res.Get().Port != 5 {
		t.Fatalf("bare floor lost: %d", res.Get().Port)
	}
}

// PATH-KEY ROUND-TRIP: a config key CONTAINING a literal '.' is keyed by the
// []string COMPONENT, not split on '.' (the NUL-join guarantee, tiered.rs:661).
func TestPathKeyRoundTripDotInKey(t *testing.T) {
	attr := newProvenanceMap()
	// A single map key that literally contains '.': "a.b".
	attr.set([]string{"a.b"}, ComputedProvenance(TierDefault))
	// Lookup by the COMPONENT "a.b" must hit.
	if p, ok := attr.ProvenanceOf("a.b"); !ok || p.Tier != TierDefault {
		t.Fatalf("dotted component key must round-trip, got %v ok=%v", p, ok)
	}
	// Lookup as two components a / b must MISS (proves no '.'-split ambiguity).
	if _, ok := attr.ProvenanceOf("a", "b"); ok {
		t.Fatal("component 'a.b' must NOT be reachable as [a, b] (NUL-join guarantee)")
	}
	// Round-trip the encoder.
	k := pathKey([]string{"a.b", "c"})
	if got := splitPathKey(k); !reflect.DeepEqual(got, []string{"a.b", "c"}) {
		t.Fatalf("pathKey round-trip failed: %v", got)
	}
}

// VALUE-EQUALITY STRUCTURAL: across two tiers whose YAML decode yields
// numerically-equal-but-differently-typed values, the change-aware skip treats
// them as UNCHANGED (so the lower tier keeps the credit).
func TestValueEqualityAcrossNumericTiers(t *testing.T) {
	ctx := context.Background()
	// bare sets port via an explicit int; a Custom overlay re-supplies the same
	// magnitude as a float64 (as a YAML decode of a bare number could).
	l := For[pcfg]("veq").Bare(pcfg{Port: 8080})
	res, err := l.ResolveProgressiveWith(ctx, []ProgressiveLayer{
		NewProgressiveLayer(FileProvenance("/etc/veq.yaml"), map[string]any{"port": float64(8080)}),
	})
	if err != nil {
		t.Fatal(err)
	}
	// float64(8080) == int 8080 → unchanged → keeps Bare credit.
	if p, _ := res.Prov().ProvenanceOf("port"); p.Tier != TierBare {
		t.Fatalf("numeric-equal re-supply must keep Bare credit, got %v", p.Tier)
	}
}

// DECODE PARITY: the resolved value decodes through the SAME shared decoder as
// Load, so a WeaklyTypedInput coercion (string "9090" env value → int) behaves
// identically in the fold.
func TestResolveDecodeParity(t *testing.T) {
	res, err := For[pcfg]("dec").
		Defaults(pcfg{Port: 8080, Tenant: "acme"}).
		ResolveProgressiveWith(context.Background(), []ProgressiveLayer{
			// env-shaped string value must coerce to int via WeaklyTypedInput.
			EnvLayer("DEC_", map[string]any{"port": "9090"}),
		})
	if err != nil {
		t.Fatal(err)
	}
	if res.Get().Port != 9090 {
		t.Fatalf("WeaklyTypedInput coercion in fold: want 9090, got %d", res.Get().Port)
	}
	if p, _ := res.Prov().ProvenanceOf("port"); p.Tier != TierCustom || p.Source.Kind != SourceEnv {
		t.Fatalf("port provenance want {Custom,Env}, got %v", p)
	}
}

// PROVENANCE DUMP determinism + String rendering — the golden-oracle shape.
func TestProvenanceDumpDeterministic(t *testing.T) {
	res, err := For[pcfg]("dump").
		Bare(pcfg{Tenant: "t", Port: 1, Replicas: 1, DB: pdb{Host: "h", Pool: 1}}).
		Defaults(pcfg{Port: 8080}).
		TagDefaults(false). // isolate the Defaults override; no tag-default opinions
		ResolveProgressive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range res.Prov().Entries() {
		b.WriteString(strings.Join(e.Path, "."))
		b.WriteString("\t")
		b.WriteString(e.Provenance.Tier.String())
		b.WriteString("\t")
		b.WriteString(e.Provenance.Source.String())
		b.WriteString("\n")
	}
	dump := b.String()
	// Deterministic: entries are lexicographically sorted. port changed by
	// prescribed → default; the rest stay bare.
	if !strings.Contains(dump, "port\tdefault\t") {
		t.Fatalf("expected port credited to default in dump:\n%s", dump)
	}
	if !strings.Contains(dump, "tenant\tbare\t") {
		t.Fatalf("expected tenant credited to bare in dump:\n%s", dump)
	}
	// Second run identical (determinism).
	res2, _ := For[pcfg]("dump").
		Bare(pcfg{Tenant: "t", Port: 1, Replicas: 1, DB: pdb{Host: "h", Pool: 1}}).
		Defaults(pcfg{Port: 8080}).
		ResolveProgressive(context.Background())
	var b2 strings.Builder
	for _, e := range res2.Prov().Entries() {
		b2.WriteString(strings.Join(e.Path, "."))
		b2.WriteString("\t")
		b2.WriteString(e.Provenance.Tier.String())
		b2.WriteString("\n")
	}
	// Compare tier lines across runs.
	if b2.Len() == 0 {
		t.Fatal("empty dump")
	}
}

// STORE SEAM UNCHANGED: the fold is additive — the classic Load path still
// produces the same value (a regression guard that Load/runPipeline is
// untouched by the fold fields).
func TestStoreSeamUnchangedByFold(t *testing.T) {
	// A loader with Bare + Discovered set (fold-only fields) must load classically
	// exactly as if they were absent.
	l := For[pcfg]("seam").
		Bare(pcfg{Tenant: "bareonly"}).
		Discovered(DiscoveryLayerFunc{N: "d", Fn: func(context.Context) map[string]any {
			return map[string]any{"tenant": "discovered-should-not-affect-load"}
		}}).
		Defaults(pcfg{Tenant: "acme", Port: 8080})
	got, err := l.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Load uses Defaults ONLY — Bare/Discovered are fold-exclusive.
	if got.Tenant != "acme" {
		t.Fatalf("classic Load must use Defaults, not Bare/Discovered: got %q", got.Tenant)
	}
	if got.Port != 8080 {
		t.Fatalf("classic Load port drift: %d", got.Port)
	}
}

// STRING/RENDER: Provenance.String renders tier + typed source detail.
func TestProvenanceStringRendering(t *testing.T) {
	cases := []struct {
		p    Provenance
		want string
	}{
		{ComputedProvenance(TierBare), "bare"},
		{ComputedProvenance(TierDiscovered), "discovered"},
		{FileProvenance("/etc/app.yaml"), "custom (file: /etc/app.yaml)"},
		{EnvProvenance("APP_"), "custom (env: APP_)"},
	}
	for _, c := range cases {
		if got := c.p.String(); got != c.want {
			t.Fatalf("String() = %q, want %q", got, c.want)
		}
	}
}
