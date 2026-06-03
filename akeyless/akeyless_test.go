package akeyless

import (
	"context"
	"testing"
)

// Resolver routes through the carrier SecretGetter and answers the "akeyless"
// backend.
func TestResolver(t *testing.T) {
	got := SecretGetterFunc(func(_ context.Context, name string) (string, error) {
		if name != "/prod/db" {
			t.Fatalf("name = %q, want /prod/db", name)
		}
		return "resolved-value", nil
	})
	r := Resolver(got)
	if r.Backend() != "akeyless" {
		t.Errorf("backend = %q, want akeyless", r.Backend())
	}
	v, err := r.Resolve(context.Background(), "/prod/db")
	if err != nil {
		t.Fatal(err)
	}
	if v != "resolved-value" {
		t.Errorf("resolve = %q, want resolved-value", v)
	}
}

// FromBootstrap is the §2.1 one-call helper and behaves like Resolver.
func TestFromBootstrap(t *testing.T) {
	r := FromBootstrap(SecretGetterFunc(func(context.Context, string) (string, error) {
		return "boot", nil
	}))
	v, _ := r.Resolve(context.Background(), "x")
	if v != "boot" {
		t.Errorf("got %q, want boot", v)
	}
}

// Profile discovery precedence: --gateway-url > profile URL > default.
func TestProfiles_Select(t *testing.T) {
	ps := Profiles{
		Active: "prod",
		Profiles: map[string]Profile{
			"prod": {GatewayURL: "https://prod.gw", Region: "us-east-1"},
			"bare": {Region: "eu-west-1"},
		},
	}

	// Active profile, no flag → profile URL.
	p, err := ps.Select("", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.GatewayURL != "https://prod.gw" {
		t.Errorf("gateway = %q, want profile URL", p.GatewayURL)
	}

	// Flag overrides profile URL.
	p, _ = ps.Select("prod", "https://flag.gw")
	if p.GatewayURL != "https://flag.gw" {
		t.Errorf("gateway = %q, want flag URL", p.GatewayURL)
	}

	// Profile without URL falls back to the default endpoint.
	p, _ = ps.Select("bare", "")
	if p.GatewayURL != DefaultGatewayURL {
		t.Errorf("gateway = %q, want %q", p.GatewayURL, DefaultGatewayURL)
	}

	// Unknown profile errors.
	if _, err := ps.Select("nope", ""); err == nil {
		t.Error("Select(nope) = nil err, want not-found error")
	}
}
