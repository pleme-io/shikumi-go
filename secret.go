package shikumi

import (
	"context"
	"fmt"
	"strings"
)

// SecretRefPrefix is the scheme that marks a config string value as a secret
// reference to be dereferenced by a SecretResolver during Load. A value such as
// "secret://akeyless//prod/db/password" is replaced, at decode time, by the
// resolved plaintext. The form is "secret://<backend>/<path>" where <backend>
// names one of the registered backends (akeyless, vault, aws, gcp, sops, env,
// command, mem). Backendless refs ("secret://<path>") use the resolver's
// default backend.
const SecretRefPrefix = "secret://"

// Secret is a redacting newtype: it carries a value of any type T but never
// reveals it through String/MarshalText/MarshalJSON/Format/GoString. Use it for
// secret-typed config fields so diagnostics, logs, and effective-config dumps
// never leak plaintext. Recover the underlying value only through Expose.
//
//	type Cfg struct {
//	    Token shikumi.Secret[string] `yaml:"token"`
//	}
//
// The zero value is a redacted empty secret. Secret implements the
// behaviour-classifying secretCarrier interface (Law 5), so external code can
// detect "this is a secret" without depending on shikumi-go's concrete type.
type Secret[T any] struct {
	value T
	set   bool
}

// NewSecret wraps a value as a redacting Secret.
func NewSecret[T any](v T) Secret[T] { return Secret[T]{value: v, set: true} }

// Expose returns the underlying secret value. This is the ONLY way to read the
// plaintext; every other accessor redacts. Call it at the point of use, never
// store the result in another config field.
func (s Secret[T]) Expose() T { return s.value }

// IsSet reports whether a value was ever assigned (distinguishes a redacted
// empty secret from an unset one).
func (s Secret[T]) IsSet() bool { return s.set }

// redacted is the single placeholder rendered in place of any secret.
const redacted = "[REDACTED]"

// String redacts. Implements fmt.Stringer.
func (s Secret[T]) String() string { return redacted }

// GoString redacts. Implements fmt.GoStringer (covers %#v).
func (s Secret[T]) GoString() string { return redacted }

// Format redacts under every fmt verb (%v, %s, %+v, %#v, %q, …), closing the
// last common leak path that String/GoString alone miss.
func (s Secret[T]) Format(f fmt.State, verb rune) {
	if verb == 'q' {
		_, _ = fmt.Fprintf(f, "%q", redacted)
		return
	}
	_, _ = f.Write([]byte(redacted))
}

// MarshalText redacts. Implements encoding.TextMarshaler, so YAML/TOML encoders
// emit the placeholder instead of plaintext.
func (s Secret[T]) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// MarshalJSON redacts. Implements json.Marshaler.
func (s Secret[T]) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// UnmarshalText accepts a decoded scalar. Only string-shaped secrets decode
// directly; for non-string T, populate via the resolver or NewSecret. This lets
// a `yaml:"token"` string field decode into Secret[string] transparently.
func (s *Secret[T]) UnmarshalText(b []byte) error {
	if v, ok := any(string(b)).(T); ok {
		s.value = v
		s.set = true
		return nil
	}
	return fmt.Errorf("shikumi: Secret[%T] cannot decode from text; use NewSecret", *new(T))
}

// secretCarrier is the behaviour-classifying carrier (Law 5): any type whose
// value must be redacted in diagnostics opts in by implementing it, without
// importing shikumi-go's concrete Secret type.
type secretCarrier interface{ secretMarker() }

func (Secret[T]) secretMarker() {}

// IsSecret reports whether v is a secret-carrying value (implements the
// secretCarrier behaviour). Diagnostics use it to redact unknown types.
func IsSecret(v any) bool {
	_, ok := v.(secretCarrier)
	return ok
}

// SecretResolver dereferences a secret reference (the part after the backend
// segment of a "secret://" ref) to its plaintext. Implementations are the
// pluggable backend seam (Akeyless/Vault/AWS/GCP/Sops/Env/Command/Mem); the
// Akeyless and cloud backends live in dep-bearing sub-packages (Law 6), while
// Env/Command/Mem ship in core (stdlib).
//
// Resolve is given the full ref path (backend-stripped) and returns plaintext.
// It mirrors the go-envconfig Mutator seam: a resolver is consulted only for
// values carrying SecretRefPrefix.
type SecretResolver interface {
	// Backend is the scheme segment this resolver answers to ("akeyless",
	// "env", "command", "mem", "sops", "vault", "aws", "gcp").
	Backend() string
	// Resolve dereferences the given backend-relative ref to plaintext.
	Resolve(ctx context.Context, ref string) (string, error)
}

// SecretBackendKind is the closed set of secret backends shikumi-go's resolver
// seam recognises — the parity image of the Rust crate's SecretBackendKind
// (Literal/Command/Op/Sops/Akeyless/Vault/AwsSecret/GcpSecret) projected onto
// the Go fleet's needs, plus Env/Mem for tests and local development.
type SecretBackendKind string

const (
	BackendAkeyless SecretBackendKind = "akeyless"
	BackendVault    SecretBackendKind = "vault"
	BackendAWS      SecretBackendKind = "aws"
	BackendGCP      SecretBackendKind = "gcp"
	BackendSops     SecretBackendKind = "sops"
	BackendEnv      SecretBackendKind = "env"
	BackendCommand  SecretBackendKind = "command"
	BackendMem      SecretBackendKind = "mem"
)

// parseSecretRef splits "secret://backend/path" into (backend, path). When no
// backend segment is present ("secret://path"), backend is "" and the caller's
// default backend applies.
func parseSecretRef(ref string) (backend, path string, ok bool) {
	if !strings.HasPrefix(ref, SecretRefPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(ref, SecretRefPrefix)
	// backend//path  or  backend/path  or  path
	if i := strings.Index(rest, "//"); i >= 0 {
		return rest[:i], rest[i+2:], true
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i], rest[i+1:], true
	}
	return "", rest, true
}
