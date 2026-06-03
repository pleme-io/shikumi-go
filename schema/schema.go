// Package schema is the dep-bearing JSON-Schema emission sub-package for
// shikumi-go (Law 6 — gated; build/diagnostics only). It turns the same typed
// config struct that drives Load into a config.schema.json via
// invopop/jsonschema, so --show-config and editor config validation fall out of
// the one typed definition for free (satisfies CFG-13's schema target). The
// core never imports invopop/jsonschema — a tool opts in by importing this
// package.
//
//	b, _ := schema.Emit[Cfg]()
//	os.WriteFile("config.schema.json", b, 0o644)
package schema

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
)

// reflector is configured to read the same `yaml` tags the loader uses, so the
// emitted schema's property names match the file keyspace exactly. Defaults are
// taken from `default:` struct tags (the same ones go-envconfig applies),
// closing the loop between the runtime applier and the published schema.
func reflector() *jsonschema.Reflector {
	r := &jsonschema.Reflector{
		// Use the yaml field tag as the JSON Schema property name so the schema
		// validates the YAML/JSON config files shikumi actually loads.
		FieldNameTag: "yaml",
		// Emit a single self-contained schema (no $defs indirection) so it is
		// easy to feed to editors and --show-config.
		DoNotReference: true,
	}
	return r
}

// Reflect returns the *jsonschema.Schema for T, for callers that want to post-
// process it before marshalling.
func Reflect[T any]() *jsonschema.Schema {
	var zero T
	return reflector().Reflect(&zero)
}

// Emit returns the indented JSON-Schema bytes for the config type T.
func Emit[T any]() ([]byte, error) {
	return json.MarshalIndent(Reflect[T](), "", "  ")
}
