package shikumi

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// Layer is one stage of the typed precedence pipeline. Each layer applies its
// values onto the shared koanf instance; later layers win (koanf's
// ordered-merge principle). The canonical fleet layer set is
//
//	defaults → flags → env → file   (then a post-merge secrets rewrite)
//
// which preserves shikumi's established precedence (file highest among the
// precedence-bearing layers). The secrets stage is not a precedence contributor:
// it runs after the merge and rewrites "secret://" refs in place (see
// secretsLayer). Explicitly-set flags still beat the file for the keys they
// carry, because koanf's posflag provider only contributes set flags.
//
// Layer is the single extension seam: akeyless/vault/consul providers are
// drop-in by implementing Apply, exactly as koanf's own providers do.
type Layer interface {
	// Name is a human label used in diagnostics.
	Name() string
	// Apply merges this layer's values into k. ctx carries cancellation for
	// network-bound layers (secret backends).
	Apply(ctx context.Context, k *koanf.Koanf) error
}

// LayerFunc adapts a function to a Layer.
type LayerFunc struct {
	N  string
	Fn func(context.Context, *koanf.Koanf) error
}

func (l LayerFunc) Name() string                                  { return l.N }
func (l LayerFunc) Apply(ctx context.Context, k *koanf.Koanf) error { return l.Fn(ctx, k) }

// --- concrete layers -------------------------------------------------------

// defaultsLayer seeds the koanf instance with a flattened defaults map. It is
// always first so every later layer overrides it.
type defaultsLayer struct{ m map[string]any }

func (d defaultsLayer) Name() string { return "defaults" }
func (d defaultsLayer) Apply(_ context.Context, k *koanf.Koanf) error {
	if len(d.m) == 0 {
		return nil
	}
	return k.Load(confmap.Provider(d.m, "."), nil)
}

// envLayer loads PREFIX_ environment variables into dotted, lowercased keys.
type envLayer struct{ prefix string }

func (e envLayer) Name() string { return "env" }
func (e envLayer) Apply(_ context.Context, k *koanf.Koanf) error {
	m := envMap(e.prefix)
	if len(m) == 0 {
		return nil
	}
	return k.Load(confmap.Provider(m, "."), nil)
}

// fileLayer loads a single config file, selecting the parser by extension. A
// missing path is a no-op (discovery handles "not found" separately).
type fileLayer struct{ path string }

func (f fileLayer) Name() string { return "file" }
func (f fileLayer) Apply(_ context.Context, k *koanf.Koanf) error {
	if f.path == "" {
		return nil
	}
	fk := koanf.New(".")
	if err := fk.Load(file.Provider(f.path), parserForLayer(f.path)); err != nil {
		return fmt.Errorf("load %q: %w", f.path, err)
	}
	return k.Load(confmap.Provider(lowerKeys(fk.Raw()), "."), nil)
}

// parserForLayer selects a koanf parser by file extension, now including JSON.
func parserForLayer(path string) koanf.Parser {
	switch {
	case strings.HasSuffix(strings.ToLower(path), ".toml"):
		return toml.Parser()
	case strings.HasSuffix(strings.ToLower(path), ".json"):
		return json.Parser()
	default:
		return yaml.Parser()
	}
}

// secretsLayer rewrites every string value carrying SecretRefPrefix by routing
// it through the matching SecretResolver (the go-envconfig Mutator seam, lifted
// to operate over a koanf instance after every precedence-bearing layer has
// merged). It runs LAST so it sees the fully-merged refs from defaults, flags,
// env, and file, and rewrites them in place — resolution is a post-merge
// rewrite, not a precedence contribution.
type secretsLayer struct {
	resolvers map[string]SecretResolver
	def       string // default backend when a ref omits its scheme segment
}

func (s secretsLayer) Name() string { return "secrets" }
func (s secretsLayer) Apply(ctx context.Context, k *koanf.Koanf) error {
	if len(s.resolvers) == 0 {
		return nil
	}
	for _, key := range k.Keys() {
		raw, ok := k.Get(key).(string)
		if !ok || !strings.HasPrefix(raw, SecretRefPrefix) {
			continue
		}
		backend, path, _ := parseSecretRef(raw)
		if backend == "" {
			backend = s.def
		}
		r, ok := s.resolvers[backend]
		if !ok {
			return fmt.Errorf("secret ref %q: no resolver registered for backend %q", raw, backend)
		}
		plain, err := r.Resolve(ctx, path)
		if err != nil {
			return fmt.Errorf("resolve secret %q: %w", raw, err)
		}
		if err := k.Set(key, plain); err != nil {
			return fmt.Errorf("set resolved secret %q: %w", key, err)
		}
	}
	return nil
}

// --- core (stdlib) secret resolvers ---------------------------------------

// EnvResolver dereferences a ref by reading the named environment variable.
// "secret://env/MY_TOKEN" → os.Getenv("MY_TOKEN"). Backend: "env".
type EnvResolver struct{}

func (EnvResolver) Backend() string { return string(BackendEnv) }
func (EnvResolver) Resolve(_ context.Context, ref string) (string, error) {
	v, ok := os.LookupEnv(ref)
	if !ok {
		return "", fmt.Errorf("env var %q not set", ref)
	}
	return v, nil
}

// CommandResolver dereferences a ref by running it as a command and capturing
// stdout (trailing newline trimmed). "secret://command/op read op://vault/item"
// → exec. Backend: "command". The seam mirrors the Rust crate's
// SecretBackend::Command.
type CommandResolver struct{}

func (CommandResolver) Backend() string { return string(BackendCommand) }
func (CommandResolver) Resolve(ctx context.Context, ref string) (string, error) {
	fields := strings.Fields(ref)
	if len(fields) == 0 {
		return "", fmt.Errorf("command secret ref is empty")
	}
	cmd := exec.CommandContext(ctx, fields[0], fields[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("command %q: %w", fields[0], err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

// MemResolver dereferences a ref from an in-memory map. Backend: "mem". It is
// the test/local backend (parity with the Rust crate's literal/in-memory path)
// and lets auth-go and unit tests plug a fake secret store with zero deps.
type MemResolver struct{ Secrets map[string]string }

func (MemResolver) Backend() string { return string(BackendMem) }
func (m MemResolver) Resolve(_ context.Context, ref string) (string, error) {
	v, ok := m.Secrets[ref]
	if !ok {
		return "", fmt.Errorf("mem secret %q not found", ref)
	}
	return v, nil
}

// Env, Command, and Mem are the convenience constructors named in §2.1 (the
// loader's Secrets(...) takes a SecretResolver).
//
//	shikumi.For[Cfg]("app").Secrets(shikumi.Env(), shikumi.Mem(seed)).Load(ctx)
func Env() SecretResolver               { return EnvResolver{} }
func Command() SecretResolver           { return CommandResolver{} }
func Mem(s map[string]string) SecretResolver { return MemResolver{Secrets: s} }
