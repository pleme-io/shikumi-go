package diag

import (
	"errors"
	"strings"
	"testing"

	shikumi "github.com/pleme-io/shikumi-go"
	"github.com/pleme-io/borealis/theme"
)

type cfg struct {
	Tenant string                 `yaml:"tenant"`
	Port   int                    `yaml:"port"`
	Token  shikumi.Secret[string] `yaml:"token"`
}

// Summary redacts secret-carrying fields and shows the rest.
func TestSummary_Redacts(t *testing.T) {
	c := cfg{Tenant: "dbk", Port: 8080, Token: shikumi.NewSecret("supersecret")}
	out := Summary(theme.Default(), c)
	if strings.Contains(out, "supersecret") {
		t.Errorf("summary leaked secret: %q", out)
	}
	if !strings.Contains(out, "REDACTED") {
		t.Errorf("summary missing REDACTED: %q", out)
	}
	if !strings.Contains(out, "dbk") || !strings.Contains(out, "8080") {
		t.Errorf("summary missing non-secret fields: %q", out)
	}
}

// Validation renders a Success row for nil and a Danger row for an error.
func TestValidation(t *testing.T) {
	ok := Validation(theme.Default(), nil)
	if !strings.Contains(ok, "valid") {
		t.Errorf("nil validation = %q, want valid", ok)
	}
	bad := Validation(theme.Default(), errors.New("boom"))
	if !strings.Contains(bad, "boom") {
		t.Errorf("err validation = %q, want boom", bad)
	}
}

// ShowConfig combines path + redacted config + validation.
func TestShowConfig(t *testing.T) {
	c := cfg{Tenant: "dbk", Token: shikumi.NewSecret("x")}
	out := ShowConfig(theme.Default(), "/etc/app/app.yaml", c, nil)
	if !strings.Contains(out, "/etc/app/app.yaml") {
		t.Errorf("missing path: %q", out)
	}
	if strings.Contains(out, "\"x\"") || strings.Contains(out, " x\n") {
		t.Errorf("possible secret leak: %q", out)
	}
}
