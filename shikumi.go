// Package shikumi is the Go representation of pleme-io's Pillar 2 — Configuration
// (仕組み, "structure/mechanism"). It mirrors the Rust `shikumi` crate's model so
// Go services and tools follow the same convention everywhere:
//
//   - XDG config discovery with an env-var path override and format preference,
//   - a typed precedence pipeline — defaults → flags → env → file (with a
//     post-merge secrets rewrite) — where later layers win (the file has the
//     highest precedence among the precedence-bearing layers, per shikumi),
//   - declarative validation co-located with the type (validator tags),
//   - secrets as a config layer, not a bolt-on (a redacting Secret[T] newtype
//     plus a pluggable SecretResolver backend seam),
//   - a lock-free, validate-before-swap, keep-last-good hot-reloadable store
//     (the ArcSwap analog).
//
// The mandate, like the Rust crate: no ad-hoc env parsing, no map[string]any
// configs. Define a struct (with `yaml` tags), discover it, load it the same
// way in every tool.
//
// # Canonical loader (§3.5)
//
// The fluent builder For[Root] is THE loader; use it once, at main:
//
//	type Cfg struct {
//		Tenant string                 `yaml:"tenant"`
//		Port   int                    `yaml:"port"`
//		Token  shikumi.Secret[string] `yaml:"token"` // never logs plaintext
//	}
//
//	cfg, err := shikumi.For[Cfg]("rebuild-db-ro").
//		EnvPrefix("REBUILD_DB_RO_").
//		EnvOverride("REBUILD_DB_RO_CONFIG").
//		Defaults(Cfg{Port: 8080}).
//		Secrets(shikumi.Env()).               // resolves secret:// refs
//		Validate(validate.New()).             // shikumi-go/validate sub-pkg
//		Load(ctx)                             // defaults→flags→env→file→secrets
//
// For a hot-reloadable service, swap Load(ctx) for LoadStore(ctx): reloads are
// validate-before-swap with keep-last-good, and Store.Watch reports
// (current-good-cfg, reloadErr).
//
// The dep-bearing features live in clearly-scoped sub-packages (Law 6):
// validate (go-playground/validator), diag (borealis-rendered diagnostics —
// the one package that imports borealis, Law 8), schema (invopop/jsonschema
// --show-config emission), flags (koanf posflag/pflag flag layer), and akeyless
// (the org-native secret backend, via a carrier interface auth-go plugs into).
//
// # Back-compat
//
// The original procedural API — Load[T], LoadStore[T], and the Discovery
// builder (New/EnvOverride/Discover) — remains as thin internals the fluent
// loader refolds under, so existing consumers keep working unchanged.
//
//	path, _ := shikumi.New("rebuild-db-ro").EnvOverride("REBUILD_DB_RO_CONFIG").Discover()
//	store, _ := shikumi.LoadStore(path, "REBUILD_DB_RO_", Cfg{Port: 8080})
//	cfg := store.Get() // *Cfg, lock-free
package shikumi

import "errors"

// ErrNotFound is returned by Discover when no config file exists in the search
// hierarchy.
var ErrNotFound = errors.New("shikumi: no config file found")
