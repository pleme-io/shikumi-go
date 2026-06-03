package validate_test

import (
	"strings"
	"testing"

	shikumi "github.com/pleme-io/shikumi-go"
	"github.com/pleme-io/shikumi-go/validate"
)

type authCfg struct {
	AuthKind      string `validate:"required,oneof=api_key file env"`
	AccessKey     string
	AccessKeyFile string
}

func TestRequiredIf(t *testing.T) {
	v := validate.New()
	validate.Rules[authCfg](v).
		RequiredIf("AccessKeyFile", "AuthKind", "file").
		Register()

	tests := []struct {
		name    string
		cfg     authCfg
		wantErr bool
	}{
		{
			name:    "file kind without file is invalid",
			cfg:     authCfg{AuthKind: "file"},
			wantErr: true,
		},
		{
			name:    "file kind with file is valid",
			cfg:     authCfg{AuthKind: "file", AccessKeyFile: "/etc/k"},
			wantErr: false,
		},
		{
			name:    "non-file kind does not require file",
			cfg:     authCfg{AuthKind: "env"},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Validate(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestMutuallyExclusive(t *testing.T) {
	v := validate.New()
	validate.Rules[authCfg](v).
		MutuallyExclusive("access", "AccessKey", "AccessKeyFile").
		Register()

	tests := []struct {
		name    string
		cfg     authCfg
		wantErr bool
	}{
		{
			name:    "neither set is allowed",
			cfg:     authCfg{AuthKind: "env"},
			wantErr: false,
		},
		{
			name:    "one set is allowed",
			cfg:     authCfg{AuthKind: "api_key", AccessKey: "k"},
			wantErr: false,
		},
		{
			name:    "both set conflicts",
			cfg:     authCfg{AuthKind: "api_key", AccessKey: "k", AccessKeyFile: "/etc/k"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// AuthKind is required by tag, so always supply a valid one.
			err := v.Validate(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestExactlyOne(t *testing.T) {
	v := validate.New()
	validate.Rules[authCfg](v).
		ExactlyOne("access", "AccessKey", "AccessKeyFile").
		Register()

	tests := []struct {
		name    string
		cfg     authCfg
		wantErr bool
	}{
		{name: "none set fails", cfg: authCfg{AuthKind: "api_key"}, wantErr: true},
		{name: "one set passes", cfg: authCfg{AuthKind: "api_key", AccessKey: "k"}, wantErr: false},
		{name: "both set fails", cfg: authCfg{AuthKind: "api_key", AccessKey: "k", AccessKeyFile: "/f"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Validate(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// typed-string enum: RequiredIf must compare the enum's underlying string.
type kind string

type enumCfg struct {
	Kind kind
	Path string
}

func TestRequiredIfTypedEnum(t *testing.T) {
	v := validate.New()
	validate.Rules[enumCfg](v).
		RequiredIf("Path", "Kind", kind("file")).
		Register()

	if err := v.Validate(enumCfg{Kind: "file"}); err == nil {
		t.Errorf("expected error: file kind requires Path")
	}
	if err := v.Validate(enumCfg{Kind: "file", Path: "/x"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := v.Validate(enumCfg{Kind: "env"}); err != nil {
		t.Errorf("unexpected error for non-file kind: %v", err)
	}
}

func TestCustomRule(t *testing.T) {
	type rangeCfg struct {
		Min int
		Max int
	}
	v := validate.New()
	validate.Rules[rangeCfg](v).
		Custom(func(c rangeCfg, report func(field, tag, param string)) {
			if c.Min > c.Max {
				report("Min", "lte_max", "")
			}
		}).
		Register()

	if err := v.Validate(rangeCfg{Min: 5, Max: 1}); err == nil {
		t.Errorf("expected error: Min > Max")
	}
	if err := v.Validate(rangeCfg{Min: 1, Max: 5}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAlias(t *testing.T) {
	type portCfg struct {
		Port int `validate:"port"`
	}
	v := validate.New().Alias("port", "gte=1,lte=65535")
	if err := v.Validate(portCfg{Port: 8080}); err != nil {
		t.Errorf("unexpected error for valid port: %v", err)
	}
	if err := v.Validate(portCfg{Port: 0}); err == nil {
		t.Errorf("expected error for out-of-range port")
	}
}

// EndpointMatrix field-level rule wires the core matrix invariants into the
// validator pass. The `endpoint_matrix` tag runs the cross-row invariants; the
// `dive` tag runs each Endpoint's own field tags.
type matrixCfg struct {
	Endpoints shikumi.EndpointMatrix `validate:"endpoint_matrix,dive"`
}

func TestEndpointMatrixValidation(t *testing.T) {
	v := validate.New()
	validate.EndpointMatrix(v)

	tests := []struct {
		name    string
		cfg     matrixCfg
		wantErr bool
		errSub  string
	}{
		{
			name: "valid matrix",
			cfg: matrixCfg{Endpoints: shikumi.EndpointMatrix{
				{Name: "gw", HostTemplate: "https://gw", Criticality: shikumi.CriticalityCritical, ExpectedCodes: []int{200}},
			}},
			wantErr: false,
		},
		{
			name: "duplicate names",
			cfg: matrixCfg{Endpoints: shikumi.EndpointMatrix{
				{Name: "gw", HostTemplate: "https://a"},
				{Name: "gw", HostTemplate: "https://b"},
			}},
			wantErr: true,
			errSub:  validate.EndpointMatrixTag,
		},
		{
			name: "missing host template",
			cfg: matrixCfg{Endpoints: shikumi.EndpointMatrix{
				{Name: "gw"},
			}},
			wantErr: true,
		},
		{
			name: "bad criticality via dive tag",
			cfg: matrixCfg{Endpoints: shikumi.EndpointMatrix{
				{Name: "gw", HostTemplate: "https://gw", Criticality: shikumi.Criticality("bogus")},
			}},
			wantErr: true,
		},
		{
			name: "out-of-range expected code via dive tag",
			cfg: matrixCfg{Endpoints: shikumi.EndpointMatrix{
				{Name: "gw", HostTemplate: "https://gw", ExpectedCodes: []int{99}},
			}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Validate(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.errSub != "" && (err == nil || !strings.Contains(err.Error(), tt.errSub)) {
				t.Errorf("error %v does not contain %q", err, tt.errSub)
			}
		})
	}
}
