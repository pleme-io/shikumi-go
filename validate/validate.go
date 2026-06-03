// Package validate is the dep-bearing validator sub-package for shikumi-go
// (Law 6 — weight import-gated). It adapts go-playground/validator/v10 (the
// de-facto declarative, struct-tag-driven validation standard used by
// gin/fiber/echo) to shikumi's small Validator seam, so the core stays
// zero-validator-dep on the happy path and a tool opts into validation only by
// importing this package.
//
//	v := validate.New() // WithRequiredStructEnabled, the 2025-forward default
//	cfg, err := shikumi.For[Cfg]("app").Validate(v).Load(ctx)
package validate

import (
	"github.com/go-playground/validator/v10"
)

// Validator wraps a *validator.Validate so it satisfies shikumi.Validator
// (its Validate(any) error method). It is the canonical implementation of the
// shikumi validation seam.
type Validator struct{ v *validator.Validate }

// New builds the fleet-standard validator with WithRequiredStructEnabled — the
// forward-compatible default that makes `required` apply to nested structs.
func New() *Validator {
	return &Validator{v: validator.New(validator.WithRequiredStructEnabled())}
}

// FromValidate adapts an already-configured *validator.Validate (e.g. one with
// custom validations registered) to the shikumi seam.
func FromValidate(v *validator.Validate) *Validator { return &Validator{v: v} }

// Underlying exposes the wrapped *validator.Validate so callers can register
// custom validations, aliases, or struct-level rules before loading.
func (a *Validator) Underlying() *validator.Validate { return a.v }

// Validate runs struct validation; it returns the raw validator.ValidationErrors
// on failure (a typed error the diag sub-package renders as a StatusList).
func (a *Validator) Validate(cfg any) error { return a.v.Struct(cfg) }
