# shikumi-go

Go representation of pleme-io's **Pillar 2 — Configuration** (仕組み). The Go
counterpart to the Rust [`shikumi`](https://github.com/pleme-io/shikumi) crate:
the same model, so every Go service and tool discovers and loads config the
same way.

> **No ad-hoc env parsing, no `map[string]any` configs.** Define a struct,
> discover it, load it — identically everywhere.

## Model

- **Discovery** — XDG config-file discovery with an env-var path override and
  format preference.
- **Provider chain** — layered `defaults → env (PREFIX_) → file`, where *later
  layers win* (the file has the highest precedence, matching the Rust crate).
  Env-var strings are coerced into the field's real type.
- **Store** — a lock-free, hot-reloadable, strongly-typed store (the ArcSwap
  analog), with a symlink-aware watcher for nix-darwin store swaps.

## Discovery precedence

1. `$APP_CONFIG` env override (the exact path), if set
2. `$XDG_CONFIG_HOME/{app}/{app}.{yaml,yml,toml}`
3. `$HOME/.config/{app}/{app}.{yaml,yml,toml}`
4. any extra `Dirs(...)` (e.g. `/etc/{app}`, a repo-local dir)
5. legacy `$HOME/.{app}`, `$HOME/.{app}.{ext}`

## Usage

```go
type Cfg struct {
    Tenant string `yaml:"tenant"`
    Port   int    `yaml:"port"`
}

path, err := shikumi.New("myapp").
    EnvOverride("MYAPP_CONFIG").
    Formats(shikumi.Yaml, shikumi.Toml).
    Discover()

// one-shot
cfg, err := shikumi.Load(path, "MYAPP_", Cfg{Port: 8080})

// or a hot-reloadable store
store, err := shikumi.LoadStore(path, "MYAPP_", Cfg{Port: 8080})
cfg := store.Get()            // *Cfg, lock-free
store.Watch(ctx, func(c *Cfg, err error) { /* reloaded */ })
defer store.Close()
```

Structs use `yaml` tags (used for both file decoding and field naming).
Decoding is case-insensitive, so env keys (lowercased, `_`-nested) map onto
camelCase tags.

## Build & test

```bash
go build ./...
go test ./...
```

Built on [`koanf`](https://github.com/knadh/koanf) (Go's Figment analog) and
[`fsnotify`](https://github.com/fsnotify/fsnotify).
