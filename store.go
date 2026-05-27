package shikumi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// structTag is the struct tag shikumi configs use. Keep it `yaml` so the same
// tags drive both file decoding and field naming.
const structTag = "yaml"

// parserFor selects a koanf parser by file extension (YAML by default).
func parserFor(path string) koanf.Parser {
	if strings.EqualFold(filepath.Ext(path), ".toml") {
		return toml.Parser()
	}
	return yaml.Parser()
}

// envMap collects PREFIX_ environment variables into dotted, lowercased keys.
// FOO_BAR_BAZ with prefix "FOO_" becomes "bar.baz" (the "_" is the nesting
// delimiter). Returns an empty map when prefix is "".
func envMap(prefix string) map[string]any {
	out := map[string]any{}
	if prefix == "" {
		return out
	}
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 || !strings.HasPrefix(kv, prefix) {
			continue
		}
		key := strings.ToLower(strings.TrimPrefix(kv[:eq], prefix))
		out[strings.ReplaceAll(key, "_", ".")] = kv[eq+1:]
	}
	return out
}

// lowerKeys recursively lowercases the keys of a nested config map, so the file
// and env layers share one namespace and precedence is deterministic. (Decoding
// into structs is case-insensitive, so this never breaks struct-tag matching.)
func lowerKeys(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok {
			v = lowerKeys(sub)
		}
		out[strings.ToLower(k)] = v
	}
	return out
}

// Load runs the shikumi provider chain once and decodes into a copy of
// defaults: defaults → env (prefix) → file, later layers winning. Fields absent
// from every layer keep their default value.
func Load[T any](path, prefix string, defaults T) (T, error) {
	out := defaults
	k := koanf.New(".")

	if m := envMap(prefix); len(m) > 0 {
		if err := k.Load(confmap.Provider(m, "."), nil); err != nil {
			return out, fmt.Errorf("shikumi: load env: %w", err)
		}
	}
	if path != "" {
		fk := koanf.New(".")
		if err := fk.Load(file.Provider(path), parserFor(path)); err != nil {
			return out, fmt.Errorf("shikumi: load %q: %w", path, err)
		}
		if err := k.Load(confmap.Provider(lowerKeys(fk.Raw()), "."), nil); err != nil {
			return out, fmt.Errorf("shikumi: merge %q: %w", path, err)
		}
	}
	// WeaklyTypedInput coerces env-var strings ("9090", "true") into the
	// field's real type, mirroring serde/figment in the Rust crate. Decoding
	// is case-insensitive, so the lowercased keys still match `yaml` tags.
	dc := &mapstructure.DecoderConfig{
		Result:           &out,
		TagName:          structTag,
		WeaklyTypedInput: true,
	}
	if err := k.UnmarshalWithConf("", &out, koanf.UnmarshalConf{DecoderConfig: dc}); err != nil {
		return out, fmt.Errorf("shikumi: decode: %w", err)
	}
	return out, nil
}

// Store is a lock-free, hot-reloadable typed config store — the Go analog of
// the Rust crate's ArcSwap store. Reads via Get never block; Reload swaps in a
// new value atomically.
type Store[T any] struct {
	path     string
	prefix   string
	defaults T

	val     atomic.Pointer[T]
	mu      sync.Mutex // serialises reloads
	watcher *fileWatcher
}

// LoadStore loads config (via Load) and returns a hot-reloadable store.
func LoadStore[T any](path, prefix string, defaults T) (*Store[T], error) {
	s := &Store[T]{path: path, prefix: prefix, defaults: defaults}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	return s, nil
}

// Get returns the current config, lock-free. Treat the returned pointer as
// read-only; Reload publishes a new pointer rather than mutating this one.
func (s *Store[T]) Get() *T { return s.val.Load() }

// Path returns the config file path backing this store.
func (s *Store[T]) Path() string { return s.path }

// Reload re-runs the provider chain and atomically swaps in the new config.
func (s *Store[T]) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := Load(s.path, s.prefix, s.defaults)
	if err != nil {
		return err
	}
	s.val.Store(&cfg)
	return nil
}
