package validate

import (
	"fmt"
	"reflect"

	"github.com/go-playground/validator/v10"
)

// This file is the conditional / cross-field validation extension (Law 6 — it
// lives in the dep-gated validate sub-package, so the shikumi core stays
// validator-free). go-playground/validator already ships the field-tag forms
// (`required_if`, `required_unless`, `excluded_with`, `oneof`, …); what it does
// NOT give you is a *reusable, programmatic, type-targeted* way to declare the
// two cross-field shapes the registry asked for — required-if and
// mutually-exclusive aliases — once, in fleet-uniform Go, against a config type.
// Rules[T] is that declarative surface. It composes onto the existing Validator
// without changing its public shape.

// Alias registers a named composite validation tag on the validator so a fleet
// can name a reusable rule once and reference it by name in struct tags
// (go-playground RegisterAlias). It returns the receiver for chaining.
//
//	v := validate.New().Alias("port", "gte=1,lte=65535")
//	// then:  Port int `validate:"port"`
func (a *Validator) Alias(name, tags string) *Validator {
	a.v.RegisterAlias(name, tags)
	return a
}

// Rules begins a type-targeted cross-field rule set for the config type T. The
// collected rules are registered as a single go-playground struct-level
// validation against T, so they run inside the same Validate pass the loader
// already invokes (in Load and before every reload swap). T must be the struct
// type the rules address (not a pointer).
//
//	validate.Rules[Cfg](v).
//	    RequiredIf("AccessKeyFile", "AuthKind", "file").
//	    MutuallyExclusive("access", "AccessKey", "AccessKeyFile").
//	    Register()
func Rules[T any](a *Validator) *RuleSet[T] {
	return &RuleSet[T]{v: a}
}

// RuleSet collects cross-field rules for one config type and registers them as
// a single struct-level validation. It is a builder: chain the rule methods,
// then call Register (which returns the underlying *Validator for further
// chaining into the loader).
type RuleSet[T any] struct {
	v     *Validator
	rules []structRule
}

// structRule is one cross-field invariant evaluated against the current struct
// value. It reports via the go-playground StructLevel so failures surface as
// ordinary ValidationErrors (rendered uniformly by the diag sub-package).
type structRule func(sl validator.StructLevel)

// RequiredIf adds the rule: field must be non-zero whenever otherField equals
// otherValue. Field names are Go struct field names (e.g. "AccessKeyFile"), not
// yaml tags. otherValue is compared by its string form, so it matches scalar
// enum fields (string/typed-string/number/bool) without reflection ceremony.
func (r *RuleSet[T]) RequiredIf(field, otherField string, otherValue any) *RuleSet[T] {
	want := stringify(otherValue)
	r.rules = append(r.rules, func(sl validator.StructLevel) {
		cur := sl.Current()
		other := fieldByName(cur, otherField)
		if !other.IsValid() || stringify(other.Interface()) != want {
			return
		}
		f := fieldByName(cur, field)
		if f.IsValid() && f.IsZero() {
			sl.ReportError(f.Interface(), field, field, "required_if", otherField+"="+want)
		}
	})
	return r
}

// RequiredUnless adds the inverse rule: field must be non-zero unless otherField
// equals otherValue.
func (r *RuleSet[T]) RequiredUnless(field, otherField string, otherValue any) *RuleSet[T] {
	want := stringify(otherValue)
	r.rules = append(r.rules, func(sl validator.StructLevel) {
		cur := sl.Current()
		other := fieldByName(cur, otherField)
		if other.IsValid() && stringify(other.Interface()) == want {
			return
		}
		f := fieldByName(cur, field)
		if f.IsValid() && f.IsZero() {
			sl.ReportError(f.Interface(), field, field, "required_unless", otherField+"="+want)
		}
	})
	return r
}

// MutuallyExclusive adds the rule: at most ONE of the named fields may be
// non-zero (mutually-exclusive aliases — e.g. inline secret vs secret-file).
// group is a label attached to the reported error so diagnostics name the
// conflicting set. When more than one field is set, every set field is reported
// so the user sees the full conflict, not just the first.
func (r *RuleSet[T]) MutuallyExclusive(group string, fields ...string) *RuleSet[T] {
	r.rules = append(r.rules, func(sl validator.StructLevel) {
		cur := sl.Current()
		var set []string
		for _, name := range fields {
			f := fieldByName(cur, name)
			if f.IsValid() && !f.IsZero() {
				set = append(set, name)
			}
		}
		if len(set) > 1 {
			for _, name := range set {
				f := fieldByName(cur, name)
				sl.ReportError(f.Interface(), name, name, "excluded_with", group)
			}
		}
	})
	return r
}

// ExactlyOne adds the rule: EXACTLY one of the named fields must be non-zero —
// a stricter sibling of MutuallyExclusive that also rejects the none-set case
// (e.g. a required choice between mutually-exclusive aliases).
func (r *RuleSet[T]) ExactlyOne(group string, fields ...string) *RuleSet[T] {
	r.rules = append(r.rules, func(sl validator.StructLevel) {
		cur := sl.Current()
		var set []string
		for _, name := range fields {
			f := fieldByName(cur, name)
			if f.IsValid() && !f.IsZero() {
				set = append(set, name)
			}
		}
		switch {
		case len(set) == 0:
			// Report against the first candidate so the namespace is concrete.
			if len(fields) > 0 {
				f := fieldByName(cur, fields[0])
				if f.IsValid() {
					sl.ReportError(f.Interface(), fields[0], fields[0], "required", group)
				}
			}
		case len(set) > 1:
			for _, name := range set {
				f := fieldByName(cur, name)
				sl.ReportError(f.Interface(), name, name, "excluded_with", group)
			}
		}
	})
	return r
}

// Custom adds an arbitrary cross-field rule. The function receives the current
// struct value (as the concrete T) and reports failures via report, a thin
// closure over StructLevel.ReportError that takes (fieldName, tag, param). This
// is the escape hatch for invariants the named helpers do not cover, kept on
// the same registration path so it renders uniformly.
func (r *RuleSet[T]) Custom(fn func(cur T, report func(fieldName, tag, param string))) *RuleSet[T] {
	r.rules = append(r.rules, func(sl validator.StructLevel) {
		cur, ok := sl.Current().Interface().(T)
		if !ok {
			return
		}
		fn(cur, func(fieldName, tag, param string) {
			f := fieldByName(sl.Current(), fieldName)
			var v any
			if f.IsValid() {
				v = f.Interface()
			}
			sl.ReportError(v, fieldName, fieldName, tag, param)
		})
	})
	return r
}

// Register installs the collected rules as one struct-level validation for T
// and returns the underlying *Validator (so the call site can chain straight
// into the loader's Validate). Calling Register with no rules is a no-op.
func (r *RuleSet[T]) Register() *Validator {
	if len(r.rules) == 0 {
		return r.v
	}
	rules := r.rules // capture
	var zero T
	r.v.v.RegisterStructValidation(func(sl validator.StructLevel) {
		for _, rule := range rules {
			rule(sl)
		}
	}, zero)
	return r.v
}

// fieldByName resolves a (possibly nested-pointer) struct value's field by its
// Go field name, diving through a single pointer indirection so it works for
// both value and pointer receivers.
func fieldByName(v reflect.Value, name string) reflect.Value {
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	return v.FieldByName(name)
}

// stringify renders a scalar comparison value to its canonical string form for
// equality comparison. It handles the common typed-string-enum case (e.g. a
// `type AuthKind string`) without the caller importing the enum type: a kind-
// String value uses its underlying string, a fmt.Stringer uses String(), and
// everything else falls back to fmt's default formatting.
func stringify(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(fmt.Stringer); ok {
		return s.String()
	}
	if rv := reflect.ValueOf(v); rv.IsValid() && rv.Kind() == reflect.String {
		return rv.String()
	}
	return fmt.Sprintf("%v", v)
}
