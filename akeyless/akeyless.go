// Package akeyless is the Akeyless secret-backend sub-package for shikumi-go
// (Law 6 — the highest-leverage, org-native backend; gated to consumers that
// resolve akeyless:// refs). It is the seam auth-go plugs into for the §2.1
// two-phase load.
//
// Crucially, this package does NOT import akeylesslabs/akeyless-go directly:
// the SDK is heavy and tracks a newer Go toolchain than the fleet pins, and a
// config primitive must stay light and offline-buildable. Instead, following
// Law 5 (behaviour-classifying CARRIER interfaces — external types opt in
// without depending on us), it defines a minimal SecretGetter interface that
// the caller's already-constructed *akeyless.V2Api session satisfies via a tiny
// adapter on the auth-go side. auth-go owns the SDK + token; shikumi-go owns the
// resolution-into-config plumbing.
//
//	// auth-go builds the session + a SecretGetter, then:
//	res := akeyless.Resolver(getter)             // a shikumi.SecretResolver
//	cfg, _ := shikumi.For[Cfg]("app").Secrets(res).Load(ctx)
package akeyless

import (
	"context"
	"fmt"

	shikumi "github.com/pleme-io/shikumi-go"
)

// SecretGetter is the carrier interface (Law 5) the Akeyless client opts into.
// auth-go's *akeyless.V2Api wrapper implements it; shikumi-go never sees the
// SDK type. ctx carries cancellation; name is the akeyless secret path
// (the backend-relative part of an "akeyless://path" ref).
type SecretGetter interface {
	GetSecretValue(ctx context.Context, name string) (string, error)
}

// SecretGetterFunc adapts a function to SecretGetter (handy for tests and for
// auth-go's thin wrapper around V2Api.GetSecretValue).
type SecretGetterFunc func(ctx context.Context, name string) (string, error)

func (f SecretGetterFunc) GetSecretValue(ctx context.Context, name string) (string, error) {
	return f(ctx, name)
}

// akeylessResolver implements shikumi.SecretResolver over a SecretGetter.
type akeylessResolver struct{ g SecretGetter }

func (akeylessResolver) Backend() string { return string(shikumi.BackendAkeyless) }
func (r akeylessResolver) Resolve(ctx context.Context, ref string) (string, error) {
	if r.g == nil {
		return "", fmt.Errorf("akeyless resolver: nil secret getter")
	}
	return r.g.GetSecretValue(ctx, ref)
}

// Resolver wraps a SecretGetter as a shikumi.SecretResolver for the "akeyless"
// backend. Pass it to shikumi.Loader.Secrets.
func Resolver(g SecretGetter) shikumi.SecretResolver { return akeylessResolver{g: g} }

// FromBootstrap is the §2.1 helper: given a SecretGetter built in phase 1 from
// credential-free bootstrap config (gateway URL + auth → session), return the
// phase-2 secret resolver. The name mirrors the theory's
// shikumi.AkeylessFromBootstrap(sess) one-call shape; auth-go supplies the
// getter from its *auth.Session.
func FromBootstrap(g SecretGetter) shikumi.SecretResolver { return Resolver(g) }

// Profile is the fleet's multi-region selector (§2.1): a typed shikumi-loaded
// record chosen by --profile from a profiles: map. Discovery precedence for the
// effective gateway URL is --gateway-url > the profile's URL > api.akeyless.io,
// implemented by Resolve below.
type Profile struct {
	GatewayURL string `yaml:"gatewayUrl"`
	AuthKind   string `yaml:"authKind"`
	AccessID   string `yaml:"accessId"`
	Region     string `yaml:"region"`
}

// DefaultGatewayURL is the public Akeyless API endpoint used when neither a
// flag nor a profile supplies one.
const DefaultGatewayURL = "https://api.akeyless.io"

// Profiles is the typed `profiles:` map a config carries; --profile selects a
// key. It is yaml-tagged so it loads through the canonical shikumi pipeline.
type Profiles struct {
	Active   string             `yaml:"active"`
	Profiles map[string]Profile `yaml:"profiles"`
}

// Select returns the named profile (or the Active one when name is empty),
// merged with the discovery precedence for the gateway URL:
// flagGatewayURL > profile.GatewayURL > DefaultGatewayURL.
func (p Profiles) Select(name, flagGatewayURL string) (Profile, error) {
	if name == "" {
		name = p.Active
	}
	var prof Profile
	if name != "" {
		var ok bool
		prof, ok = p.Profiles[name]
		if !ok {
			return Profile{}, fmt.Errorf("akeyless: profile %q not found", name)
		}
	}
	switch {
	case flagGatewayURL != "":
		prof.GatewayURL = flagGatewayURL
	case prof.GatewayURL == "":
		prof.GatewayURL = DefaultGatewayURL
	}
	return prof, nil
}
