package schema

import (
	"strings"
	"testing"
)

type cfg struct {
	Tenant string `yaml:"tenant"`
	Port   int    `yaml:"port" default:"8080"`
}

// Emit produces a JSON Schema keyed on the yaml field names.
func TestEmit(t *testing.T) {
	b, err := Emit[cfg]()
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"tenant"`) {
		t.Errorf("schema missing yaml-tagged property 'tenant': %s", s)
	}
	if !strings.Contains(s, `"port"`) {
		t.Errorf("schema missing yaml-tagged property 'port': %s", s)
	}
	if !strings.Contains(s, "$schema") {
		t.Errorf("schema missing $schema header: %s", s)
	}
}
