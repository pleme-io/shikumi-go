package shikumi

import (
	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/maps"
)

// flattenDefaults converts a typed defaults struct into a flat, dotted,
// lowercased map suitable for confmap so it can seed the koanf instance as the
// lowest-precedence layer. It honours `yaml` struct tags (structTag) so the
// keyspace matches the file/env layers exactly.
//
// We decode the struct to a nested map[string]any via mapstructure (round-trip
// through the same tag the file decoder uses), then flatten with koanf's own
// maps.Flatten so nested keys become "a.b.c". This keeps the defaults layer in
// the same namespace as every other layer — the prerequisite for deterministic
// precedence.
func flattenDefaults(v any) map[string]any {
	nested := map[string]any{}
	dc := &mapstructure.DecoderConfig{
		Result:  &nested,
		TagName: structTag,
	}
	dec, err := mapstructure.NewDecoder(dc)
	if err != nil {
		return map[string]any{}
	}
	if err := dec.Decode(v); err != nil {
		return map[string]any{}
	}
	nested = lowerKeys(nested)
	flat, _ := maps.Flatten(nested, nil, ".")
	return flat
}
