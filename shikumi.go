// Package shikumi is the Go representation of pleme-io's Pillar 2 — Configuration
// (仕組み, "structure/mechanism"). It mirrors the Rust `shikumi` crate's model so
// Go services and tools follow the same convention everywhere:
//
//   - XDG config discovery with an env-var path override and format preference,
//   - a layered provider chain — defaults → env (PREFIX_) → file — where later
//     layers win (the file has the highest precedence, per shikumi),
//   - a lock-free, hot-reloadable, strongly-typed store (the ArcSwap analog).
//
// The mandate, like the Rust crate: no ad-hoc env parsing, no map[string]any
// configs. Define a struct (with `yaml` tags), discover it, load it the same
// way in every tool.
//
//	type Cfg struct {
//		Tenant string `yaml:"tenant"`
//		Port   int    `yaml:"port"`
//	}
//
//	path, _ := shikumi.New("rebuild-db-ro").EnvOverride("REBUILD_DB_RO_CONFIG").Discover()
//	store, _ := shikumi.LoadStore(path, "REBUILD_DB_RO_", Cfg{Port: 8080})
//	cfg := store.Get() // *Cfg, lock-free
package shikumi

import "errors"

// ErrNotFound is returned by Discover when no config file exists in the search
// hierarchy.
var ErrNotFound = errors.New("shikumi: no config file found")
