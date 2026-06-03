// Package diag is the borealis-rendered diagnostics LEAF for shikumi-go (Law 8).
// It is the ONLY shikumi-go package that imports borealis; the shikumi-go core
// never does, which breaks the shikumi↔borealis dependency cycle (shikumi
// diagnostics need borealis; borealis config needs shikumi — each cross-cut
// lives in a third leaf, here and in borealis/cfg respectively).
//
// It renders three things through borealis (Render-to-string, never to a fixed
// io.Writer):
//   - a discovery/effective-config summary (comp.KV), with secret-carrying
//     fields redacted via shikumi.IsSecret,
//   - validation failures as a comp.StatusList,
//   - a --show-config section combining both.
package diag

import (
	"fmt"
	"reflect"
	"sort"

	shikumi "github.com/pleme-io/shikumi-go"
	"github.com/pleme-io/borealis/comp"
	"github.com/pleme-io/borealis/theme"

	"github.com/go-playground/validator/v10"
)

// Summary renders the effective configuration as aligned key/value pairs,
// redacting any secret-carrying field so plaintext never reaches a diagnostic
// surface. It walks the struct via reflection so it works for any Root.
func Summary(t theme.Theme, cfg any) string {
	pairs := kvPairs("", reflect.ValueOf(cfg))
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].K < pairs[j].K })
	return comp.KV(t, pairs)
}

// kvPairs flattens a struct into dotted key/value Pairs, honouring the yaml
// tag for key names and redacting secret-carrying values.
func kvPairs(prefix string, v reflect.Value) []comp.Pair {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	var out []comp.Pair
	switch v.Kind() {
	case reflect.Struct:
		// Secret types are structs; redact whole-value rather than descending.
		if v.CanInterface() && shikumi.IsSecret(v.Interface()) {
			return []comp.Pair{{K: prefix, V: "[REDACTED]"}}
		}
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" { // unexported
				continue
			}
			name := f.Tag.Get("yaml")
			if name == "" || name == "-" {
				name = f.Name
			}
			key := name
			if prefix != "" {
				key = prefix + "." + name
			}
			out = append(out, kvPairs(key, v.Field(i))...)
		}
	default:
		val := "<unset>"
		if v.CanInterface() {
			iv := v.Interface()
			if shikumi.IsSecret(iv) {
				val = "[REDACTED]"
			} else {
				val = fmt.Sprintf("%v", iv)
			}
		}
		out = append(out, comp.Pair{K: prefix, V: val})
	}
	return out
}

// Validation renders a validation error as a borealis StatusList. It understands
// go-playground/validator's ValidationErrors (one Danger row per failed field)
// and falls back to a single Danger row for any other error. A nil error
// renders one Success row.
func Validation(t theme.Theme, err error) string {
	if err == nil {
		return comp.StatusList(t, []comp.Item{{Role: theme.Success, Label: "config", Detail: "valid"}})
	}
	var verrs validator.ValidationErrors
	if asValidationErrors(err, &verrs) {
		items := make([]comp.Item, 0, len(verrs))
		for _, fe := range verrs {
			items = append(items, comp.Item{
				Role:   theme.Danger,
				Label:  fe.Namespace(),
				Detail: fmt.Sprintf("failed %q (got %v)", fe.Tag(), fe.Value()),
			})
		}
		return comp.StatusList(t, items)
	}
	return comp.StatusList(t, []comp.Item{{Role: theme.Danger, Label: "config", Detail: err.Error()}})
}

// ShowConfig is the --show-config surface: the discovery path, the redacted
// effective config, and the validation result, rendered as one block.
func ShowConfig(t theme.Theme, path string, cfg any, validationErr error) string {
	head := comp.KV(t, []comp.Pair{{K: "config", V: pathOrNone(path)}})
	return head + "\n\n" + Summary(t, cfg) + "\n\n" + Validation(t, validationErr)
}

func pathOrNone(p string) string {
	if p == "" {
		return "(none — defaults + env)"
	}
	return p
}

// asValidationErrors unwraps err into a validator.ValidationErrors if possible.
func asValidationErrors(err error, dst *validator.ValidationErrors) bool {
	if ve, ok := err.(validator.ValidationErrors); ok {
		*dst = ve
		return true
	}
	// Loader wraps validation errors with %w, so try unwrapping one level.
	type unwrapper interface{ Unwrap() error }
	for e := err; e != nil; {
		if ve, ok := e.(validator.ValidationErrors); ok {
			*dst = ve
			return true
		}
		u, ok := e.(unwrapper)
		if !ok {
			break
		}
		e = u.Unwrap()
	}
	return false
}
