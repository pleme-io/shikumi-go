package validate

import (
	"github.com/go-playground/validator/v10"
	shikumi "github.com/pleme-io/shikumi-go"
)

// EndpointMatrixTag is the field-level validation tag name registered by
// EndpointMatrix. Apply it to a field whose type is shikumi.EndpointMatrix so
// the matrix-level invariants (non-empty name + host_template, unique names)
// run inside the validator pass:
//
//	type Cfg struct {
//	    Endpoints shikumi.EndpointMatrix `yaml:"endpoints" validate:"endpoint_matrix,dive"`
//	}
//
// The `dive` portion validates each Endpoint's own field tags (oneof
// criticality, expected-code range); the `endpoint_matrix` portion validates
// the cross-row invariants the per-element tags cannot express.
const EndpointMatrixTag = "endpoint_matrix"

// EndpointMatrix registers the EndpointMatrixTag field validator on the
// validator, wrapping the zero-dependency shikumi.EndpointMatrix.Validate so
// both the validator path and a direct call share exactly one implementation
// (PRIME DIRECTIVE — one shape). go-playground struct-level validations only
// fire for struct types, and an EndpointMatrix is a slice; a field-level tag is
// the idiomatic way to attach a slice-level cross-row rule. Returns the receiver
// for chaining.
//
//	v := validate.EndpointMatrix(validate.New())
//	cfg, _ := shikumi.For[Cfg]("app").Validate(v).Load(ctx)
func EndpointMatrix(a *Validator) *Validator {
	_ = a.v.RegisterValidation(EndpointMatrixTag, func(fl validator.FieldLevel) bool {
		m, ok := fl.Field().Interface().(shikumi.EndpointMatrix)
		if !ok {
			// Tag applied to the wrong type — treat as a pass so the misuse
			// surfaces as a compile/review issue, not a silent validation block.
			return true
		}
		return m.Validate() == nil
	})
	return a
}
