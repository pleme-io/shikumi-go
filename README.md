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
- **Typed precedence pipeline** — `defaults → flags → env → file` (then a
  post-merge `secrets` rewrite), where *later layers win* (the file has the
  highest precedence among the precedence-bearing layers, matching the Rust
  crate). Env-var strings are coerced into the field's real type. Each stage is
  a typed `Layer`, so akeyless/vault/consul providers are drop-in.
- **Validation** — declarative invariants co-located with the type
  (go-playground/validator tags), run in `Load` *and* before every reload swap.
- **Secrets as a layer** — a redacting `Secret[T]` newtype (never logs
  plaintext) plus a pluggable `SecretResolver` backend seam
  (Akeyless/Vault/AWS/GCP/Sops/Env/Command/Mem).
- **Store** — a lock-free, **validate-before-swap, keep-last-good**,
  hot-reloadable store (the ArcSwap analog), with a symlink-aware watcher for
  nix-darwin store swaps. A bad reload is rejected; the last-good config is kept.

## Canonical loader (`For[Root]`)

The fluent builder is **the** loader — use it once, at `main`:

```go
type Cfg struct {
    Tenant string                 `yaml:"tenant"`
    Port   int                    `yaml:"port"`
    Token  shikumi.Secret[string] `yaml:"token"`             // redacts everywhere
    Region string                 `yaml:"region" env:"REGION, default=us-east-1"`
}

cfg, err := shikumi.For[Cfg]("myapp").
    EnvPrefix("MYAPP_").
    EnvOverride("MYAPP_CONFIG").
    Defaults(Cfg{Port: 8080}).
    Secrets(shikumi.Env()).                 // resolves secret://env/NAME refs
    Validate(validate.New()).               // shikumi-go/validate sub-package
    Load(ctx)                               // defaults→flags→env→file→secrets

// hot-reloadable, validate-before-swap, keep-last-good:
store, err := shikumi.For[Cfg]("myapp").Validate(validate.New()).LoadStore(ctx)
store.Watch(ctx, func(c *Cfg, reloadErr error) { /* c is always last-good */ })
```

### Conditional / cross-field validation

Beyond the per-field `validate` tags, the `validate` sub-package adds a typed,
reusable surface for the cross-field shapes struct tags express awkwardly —
**required-if** and **mutually-exclusive aliases** — declared once against a
config type and run inside the same `Validate` pass the loader already invokes:

```go
v := validate.New().Alias("port", "gte=1,lte=65535") // reusable named tag
validate.Rules[Cfg](v).
    RequiredIf("AccessKeyFile", "AuthKind", "file").       // file kind ⇒ file required
    MutuallyExclusive("access", "AccessKey", "AccessKeyFile"). // at most one
    ExactlyOne("creds", "InlineKey", "KeyFile").            // exactly one
    Register()
cfg, _ := shikumi.For[Cfg]("app").Validate(v).Load(ctx)
```

`Custom` is the escape hatch for invariants the named helpers do not cover; every
rule reports through go-playground `ValidationErrors`, so failures render the same
way as ordinary tag violations (via the `diag` sub-package).

### Endpoint matrix

`shikumi.Endpoint` / `shikumi.EndpointMatrix` are zero-dependency, embeddable
core types for the recurring "list of endpoints this tool must reach" shape
(host-template + path + criticality + expected-codes). Embed the matrix in your
config and author it in YAML:

```go
type Cfg struct {
    Region    string                 `yaml:"region"`
    Endpoints shikumi.EndpointMatrix `yaml:"endpoints" validate:"endpoint_matrix,dive"`
}
```

```yaml
endpoints:
  - name: gateway
    host_template: "https://{region}.gateway.example.com"
    path: "/v2/health"
    criticality: critical
    expected_codes: [200, 204]
```

```go
url := ep.Expand(map[string]string{"region": cfg.Region}) // host-template expansion
ok  := ep.Accepts(resp.StatusCode)                         // expected-code check
crit := cfg.Endpoints.Critical()                           // criticality filtering
```

The `endpoint_matrix` field-validator (registered via `validate.EndpointMatrix(v)`)
enforces the cross-row invariants (non-empty name + host-template, unique names);
the `dive` portion validates each endpoint's own tags (criticality `oneof`,
expected-code range).

### Secrets

Config string values of the form `secret://<backend>/<path>` are dereferenced at
load time by the matching resolver and land in a redacting `Secret[T]`:

```yaml
token: secret://akeyless//prod/db/password   # akeyless backend
api:   secret://env/MY_API_KEY               # env backend
op:    secret://command/op read op://v/i     # command backend
```

Core ships `Env()`, `Command()`, `Mem(map)` (stdlib, zero-dep). The org-native
**Akeyless** backend lives in `shikumi-go/akeyless` and plugs in via a carrier
interface — `auth-go` supplies the client; shikumi-go never imports the SDK.

### Two-phase load (self-referential backends)

When the secret backend's own credentials come *from* config, run phase 1
credential-free, build the client, then resolve in phase 2:

```go
boot, _ := shikumi.For[Boot]("app").EnvPrefix("APP_").LoadBootstrap(ctx)
sess    := auth.Resolve(ctx, boot)                 // build client from boot
cfg,  _ := shikumi.For[Cfg]("app").EnvPrefix("APP_").
    Secrets(akeyless.FromBootstrap(sess)).Validate(v).Load(ctx)
```

## Sub-packages (dep-gated, Law 6)

| Package | Dep | Purpose |
|---|---|---|
| `validate` | go-playground/validator/v10 | declarative struct-tag validation |
| `diag` | pleme-io/borealis | borealis-rendered `--show-config`, redacted summary, validation StatusList (the **only** package importing borealis — Law 8) |
| `schema` | invopop/jsonschema | emit `config.schema.json` for `--show-config` + IDE validation |
| `flags` | knadh/koanf posflag + spf13/pflag | bind CLI flags through the same chain |
| `akeyless` | *(carrier interface only)* | org-native secret backend seam for auth-go |

The **core** stays light: only koanf, mapstructure, fsnotify, and
go-envconfig (whose `default:` applier is reused, not reimplemented).

## Discovery precedence

1. `$APP_CONFIG` env override (the exact path), if set
2. `$XDG_CONFIG_HOME/{app}/{app}.{yaml,yml,toml}`
3. `$HOME/.config/{app}/{app}.{yaml,yml,toml}`
4. any extra `Dirs(...)` (e.g. `/etc/{app}`, a repo-local dir)
5. legacy `$HOME/.{app}`, `$HOME/.{app}.{ext}`

## Back-compat (procedural API)

The original API remains as thin internals the fluent loader refolds under, so
existing consumers keep working unchanged:

```go
path, _  := shikumi.New("myapp").EnvOverride("MYAPP_CONFIG").Discover()
cfg, _   := shikumi.Load(path, "MYAPP_", Cfg{Port: 8080})         // one-shot
store, _ := shikumi.LoadStore(path, "MYAPP_", Cfg{Port: 8080})    // hot-reload
cfg := store.Get()            // *Cfg, lock-free
store.Watch(ctx, func(c *Cfg, err error) { /* reloaded, last-good kept */ })
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
