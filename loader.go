package shikumi

import (
	"context"
	"fmt"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/v2"
	"github.com/sethvargo/go-envconfig"
)

// Validator is the validation seam: anything that can validate a decoded config
// value. The canonical implementation is go-playground/validator/v10, adapted
// in the shikumi-go/validate sub-package, but the core depends only on this
// tiny interface so a tool that passes no validator pays zero dependency weight
// (Law 6). Validate is invoked in Load AND before every Reload swap.
type Validator interface {
	Validate(v any) error
}

// ValidatorFunc adapts a plain function to the Validator seam.
type ValidatorFunc func(any) error

func (f ValidatorFunc) Validate(v any) error { return f(v) }

// Loader is the canonical fluent config loader (§3.5): the ONE loader the fleet
// uses at main. It composes a typed precedence pipeline
// (defaults → flags → env → secrets → file) over a single koanf instance,
// validates the decoded result, and (via Store) supports validate-before-swap
// keep-last-good hot reload. The legacy Load[T]/LoadStore[T] refold under this
// builder and remain as thin back-compat internals.
//
//	cfg, err := shikumi.For[Cfg]("rebuild-db-ro").
//	    EnvPrefix("REBUILD_DB_RO_").
//	    EnvOverride("REBUILD_DB_RO_CONFIG").
//	    Defaults(Cfg{Port: 8080}).
//	    Secrets(shikumi.Env()).
//	    Validate(v).
//	    Load(ctx)
type Loader[Root any] struct {
	app         string
	envPrefix   string
	envOverride string
	formats     []Format
	dirs        []string
	explicitPath string

	defaults  Root
	hasDefaults bool

	preLayers  []Layer // layers before env (e.g. flags)
	resolvers  map[string]SecretResolver
	defBackend string
	bootstrap  []Layer // BootstrapLayer phase-1 layers

	validators []Validator
	applyTagDefaults bool
}

// Option is a functional option for the fluent loader (item 7 — functional
// options over a fixed builder). The chained methods below are sugar that build
// Options, so both styles compose.
type Option[Root any] func(*Loader[Root])

// For starts the canonical loader for the named app. Pass Options to configure
// declaratively, or chain the fluent methods.
func For[Root any](app string, opts ...Option[Root]) *Loader[Root] {
	l := &Loader[Root]{
		app:        app,
		formats:    DefaultFormats,
		resolvers:  map[string]SecretResolver{},
		defBackend: string(BackendEnv),
		applyTagDefaults: true,
	}
	for _, o := range opts {
		o(l)
	}
	return l
}

// EnvPrefix sets the PREFIX_ used by the env layer.
func (l *Loader[Root]) EnvPrefix(p string) *Loader[Root] { l.envPrefix = p; return l }

// EnvOverride names the env var whose value, if set, is the exact config path
// (skips discovery).
func (l *Loader[Root]) EnvOverride(name string) *Loader[Root] { l.envOverride = name; return l }

// Formats sets the discovery format preference order.
func (l *Loader[Root]) Formats(fs ...Format) *Loader[Root] {
	if len(fs) > 0 {
		l.formats = fs
	}
	return l
}

// Dirs appends extra discovery directories.
func (l *Loader[Root]) Dirs(dirs ...string) *Loader[Root] {
	l.dirs = append(l.dirs, dirs...)
	return l
}

// Path forces the config file path, bypassing discovery (used by tests and
// callers that already resolved a path).
func (l *Loader[Root]) Path(p string) *Loader[Root] { l.explicitPath = p; return l }

// Defaults seeds the pipeline with a typed defaults value (lowest precedence).
func (l *Loader[Root]) Defaults(d Root) *Loader[Root] {
	l.defaults = d
	l.hasDefaults = true
	return l
}

// Layers appends arbitrary pre-env layers (e.g. the koanf flag layer from the
// shikumi-go/flags sub-package). They sit between defaults and env so the
// canonical order defaults → flags → env → secrets → file holds.
func (l *Loader[Root]) Layers(ls ...Layer) *Loader[Root] {
	l.preLayers = append(l.preLayers, ls...)
	return l
}

// Secrets registers one or more secret resolvers; their refs are dereferenced
// in the "secrets" stage. The first resolver becomes the default backend for
// scheme-less refs.
func (l *Loader[Root]) Secrets(rs ...SecretResolver) *Loader[Root] {
	for i, r := range rs {
		l.resolvers[r.Backend()] = r
		if i == 0 {
			l.defBackend = r.Backend()
		}
	}
	return l
}

// Bootstrap registers phase-1 (credential-free) layers that resolve BEFORE any
// secret layer, so a backend client can be built from config that itself
// contains no secrets (the two-phase load for self-referential backends, §2.1).
// In practice the caller runs LoadBootstrap, builds the client, then calls
// Secrets(...) and Load.
func (l *Loader[Root]) Bootstrap(ls ...Layer) *Loader[Root] {
	l.bootstrap = append(l.bootstrap, ls...)
	return l
}

// Validate registers a validator run in Load and before every Reload swap.
func (l *Loader[Root]) Validate(v Validator) *Loader[Root] {
	if v != nil {
		l.validators = append(l.validators, v)
	}
	return l
}

// TagDefaults toggles the go-envconfig `default:` struct-tag applier (on by
// default). Reusing go-envconfig's applier rather than reimplementing it is the
// PRIME-DIRECTIVE-correct choice (the library is already in the closure via the
// Mutator seam).
func (l *Loader[Root]) TagDefaults(on bool) *Loader[Root] { l.applyTagDefaults = on; return l }

// --- functional Options (mirror the fluent methods) ------------------------

func WithEnvPrefix[Root any](p string) Option[Root]   { return func(l *Loader[Root]) { l.envPrefix = p } }
func WithEnvOverride[Root any](n string) Option[Root] { return func(l *Loader[Root]) { l.envOverride = n } }
func WithDefaults[Root any](d Root) Option[Root] {
	return func(l *Loader[Root]) { l.defaults = d; l.hasDefaults = true }
}
func WithDirs[Root any](dirs ...string) Option[Root] {
	return func(l *Loader[Root]) { l.dirs = append(l.dirs, dirs...) }
}
func WithSecrets[Root any](rs ...SecretResolver) Option[Root] {
	return func(l *Loader[Root]) { l.Secrets(rs...) }
}
func WithValidator[Root any](v Validator) Option[Root] {
	return func(l *Loader[Root]) { l.Validate(v) }
}
func WithLayers[Root any](ls ...Layer) Option[Root] {
	return func(l *Loader[Root]) { l.preLayers = append(l.preLayers, ls...) }
}

// --- resolution ------------------------------------------------------------

// resolvePath returns the discovered (or explicit) config path. ErrNotFound is
// NOT an error here — a tool may run on defaults+env alone; callers wanting a
// hard requirement check the returned path.
func (l *Loader[Root]) resolvePath() (string, error) {
	if l.explicitPath != "" {
		return l.explicitPath, nil
	}
	d := New(l.app).Formats(l.formats...).Dirs(l.dirs...)
	if l.envOverride != "" {
		d = d.EnvOverride(l.envOverride)
	}
	p, err := d.Discover()
	if err == ErrNotFound {
		return "", nil
	}
	return p, err
}

// pipeline builds the ordered Layer chain. The precedence-bearing stages run in
// the canonical fleet order defaults → flags → env → file (later wins → file
// has highest precedence, the shikumi convention). The secrets stage is special:
// it does not contribute precedence — it REWRITES "secret://" refs that survived
// the precedence merge to their plaintext — so it must run LAST, after every
// layer that could carry a ref (refs almost always live in the file). The plan's
// "defaults→flags→env→secrets→file" names the conceptual layer set; resolution
// is correctly a post-merge rewrite, the go-envconfig Mutator seam lifted over
// the merged koanf instance.
func (l *Loader[Root]) pipeline(path string) []Layer {
	var ls []Layer
	if l.hasDefaults {
		ls = append(ls, defaultsLayer{m: flattenDefaults(l.defaults)})
	}
	ls = append(ls, l.preLayers...)
	if l.envPrefix != "" {
		ls = append(ls, envLayer{prefix: l.envPrefix})
	}
	ls = append(ls, fileLayer{path: path})
	if len(l.resolvers) > 0 {
		ls = append(ls, secretsLayer{resolvers: l.resolvers, def: l.defBackend})
	}
	return ls
}

// runPipeline applies every layer onto a fresh koanf and decodes into a Root.
func (l *Loader[Root]) runPipeline(ctx context.Context, path string) (Root, error) {
	var out Root
	if l.hasDefaults {
		out = l.defaults
	}
	k := koanf.New(".")
	for _, layer := range l.pipeline(path) {
		if err := layer.Apply(ctx, k); err != nil {
			return out, fmt.Errorf("shikumi: layer %q: %w", layer.Name(), err)
		}
	}
	if err := k.UnmarshalWithConf("", &out, koanf.UnmarshalConf{Tag: structTag, DecoderConfig: decoderConfig(&out)}); err != nil {
		return out, fmt.Errorf("shikumi: decode: %w", err)
	}
	// Apply `default:` struct tags for fields untouched by any layer, reusing
	// go-envconfig's applier (no stdlib reimpl — PRIME DIRECTIVE).
	if l.applyTagDefaults {
		if err := applyTagDefaults(ctx, &out); err != nil {
			return out, fmt.Errorf("shikumi: apply defaults: %w", err)
		}
	}
	return out, nil
}

// validate runs every registered validator over v, returning the first error.
func (l *Loader[Root]) validate(v Root) error {
	for _, val := range l.validators {
		if err := val.Validate(v); err != nil {
			return err
		}
	}
	return nil
}

// Load runs the full pipeline once, validates, and returns the config. This is
// the canonical fleet loader call (§3.5).
func (l *Loader[Root]) Load(ctx context.Context) (Root, error) {
	path, err := l.resolvePath()
	if err != nil {
		var zero Root
		return zero, err
	}
	out, err := l.runPipeline(ctx, path)
	if err != nil {
		return out, err
	}
	if err := l.validate(out); err != nil {
		return out, fmt.Errorf("shikumi: validate: %w", err)
	}
	return out, nil
}

// LoadBootstrap runs ONLY the credential-free phase-1 pipeline (defaults →
// bootstrap layers → env → file, no secret resolution), for the two-phase load
// (§2.1). Build the backend client from the returned value, then call Secrets
// and Load for phase 2.
func (l *Loader[Root]) LoadBootstrap(ctx context.Context) (Root, error) {
	path, err := l.resolvePath()
	if err != nil {
		var zero Root
		return zero, err
	}
	var out Root
	if l.hasDefaults {
		out = l.defaults
	}
	k := koanf.New(".")
	var ls []Layer
	if l.hasDefaults {
		ls = append(ls, defaultsLayer{m: flattenDefaults(l.defaults)})
	}
	ls = append(ls, l.bootstrap...)
	if l.envPrefix != "" {
		ls = append(ls, envLayer{prefix: l.envPrefix})
	}
	ls = append(ls, fileLayer{path: path})
	for _, layer := range ls {
		if err := layer.Apply(ctx, k); err != nil {
			return out, fmt.Errorf("shikumi: bootstrap layer %q: %w", layer.Name(), err)
		}
	}
	if err := k.UnmarshalWithConf("", &out, koanf.UnmarshalConf{Tag: structTag, DecoderConfig: decoderConfig(&out)}); err != nil {
		return out, fmt.Errorf("shikumi: bootstrap decode: %w", err)
	}
	if l.applyTagDefaults {
		if err := applyTagDefaults(ctx, &out); err != nil {
			return out, fmt.Errorf("shikumi: bootstrap defaults: %w", err)
		}
	}
	return out, nil
}

// LoadStore runs the pipeline and returns a validate-before-swap,
// keep-last-good hot-reloadable Store bound to this loader.
func (l *Loader[Root]) LoadStore(ctx context.Context) (*Store[Root], error) {
	path, err := l.resolvePath()
	if err != nil {
		return nil, err
	}
	s := &Store[Root]{
		path:     path,
		prefix:   l.envPrefix,
		defaults: l.defaults,
		loader:   l,
	}
	if err := s.reloadCtx(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// decoderConfig is the ONE mapstructure config shikumi uses everywhere (the
// fluent pipeline, bootstrap, and the legacy Load), so decode semantics never
// drift between entry points (PRIME DIRECTIVE — one shape). It sets:
//   - TagName=structTag ("yaml") so the same tags drive file decode and naming,
//   - WeaklyTypedInput so env-var strings coerce into int/bool/etc.,
//   - the TextUnmarshaller hook so a string value decodes into any
//     encoding.TextUnmarshaler field — notably Secret[string], whose
//     UnmarshalText accepts the (already secret-resolved) plaintext.
func decoderConfig(out any) *mapstructure.DecoderConfig {
	return &mapstructure.DecoderConfig{
		Result:           out,
		TagName:          structTag,
		WeaklyTypedInput: true,
		DecodeHook:       mapstructure.TextUnmarshallerHookFunc(),
	}
}

// applyTagDefaults applies go-envconfig's `env:"NAME, default=…"` defaults
// using a lookuper that resolves nothing (so only the inline defaults fire).
// This REUSES go-envconfig's default applier rather than reimplementing a
// stdlib default-tag walker (PRIME DIRECTIVE: the library is already in the
// closure via the secret Mutator seam). go-envconfig sets a default only when
// the env lookup misses and the field is still its zero value, so a value any
// prior pipeline layer set is preserved (default never clobbers a loaded value).
func applyTagDefaults[T any](ctx context.Context, out *T) error {
	return envconfig.ProcessWith(ctx, &envconfig.Config{
		Target:   out,
		Lookuper: envconfig.MapLookuper(map[string]string{}), // resolves nothing → only defaults fire
	})
}
