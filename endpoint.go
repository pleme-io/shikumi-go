package shikumi

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Criticality classifies how load-bearing an endpoint is, so consumers
// (preflight/doctor checks, readiness probes, dashboards) can treat a failed
// CRITICAL endpoint as fatal while a failed LOW endpoint is informational. It
// is a closed string enum so it round-trips through YAML/env unchanged and
// validates with the `oneof` tag.
type Criticality string

const (
	// CriticalityCritical — failure should fail the whole check / block startup.
	CriticalityCritical Criticality = "critical"
	// CriticalityHigh — failure is a hard warning that usually escalates.
	CriticalityHigh Criticality = "high"
	// CriticalityMedium — failure degrades but does not block.
	CriticalityMedium Criticality = "medium"
	// CriticalityLow — failure is informational only.
	CriticalityLow Criticality = "low"
)

// rank orders criticality from most to least severe (critical = 0). Unknown
// values sort last so a malformed matrix never masks a real critical row.
func (c Criticality) rank() int {
	switch c {
	case CriticalityCritical:
		return 0
	case CriticalityHigh:
		return 1
	case CriticalityMedium:
		return 2
	case CriticalityLow:
		return 3
	default:
		return 4
	}
}

// IsCritical reports whether this criticality is the most-severe band.
func (c Criticality) IsCritical() bool { return c == CriticalityCritical }

// String implements fmt.Stringer.
func (c Criticality) String() string { return string(c) }

// Endpoint is a reusable, embeddable schema type describing ONE service
// endpoint a tool must reach: a host-template (with `{var}` placeholders a
// consumer expands per-environment), a request path, a criticality band, and
// the set of HTTP status codes considered healthy. It is a zero-dependency core
// type — it carries both `yaml` tags (so the shikumi loader decodes it) and
// go-playground `validate` tags (so the shikumi-go/validate sub-package can
// enforce it) without the core importing any validator dependency (Law 6).
//
// Consumers embed an EndpointMatrix in their own shikumi Config rather than
// hand-rolling a per-tool endpoint list:
//
//	type Cfg struct {
//	    Region    string             `yaml:"region"`
//	    Endpoints shikumi.EndpointMatrix `yaml:"endpoints" validate:"dive"`
//	}
//
// and author it declaratively in YAML:
//
//	endpoints:
//	  - name: gateway
//	    host_template: "https://{region}.gateway.example.com"
//	    path: "/v2/health"
//	    criticality: critical
//	    expected_codes: [200, 204]
type Endpoint struct {
	// Name uniquely identifies the endpoint within a matrix (used for lookups,
	// diagnostics, and dedup). Required.
	Name string `yaml:"name" json:"name" validate:"required"`
	// HostTemplate is the scheme+host (and optional base path) with `{var}`
	// placeholders a consumer expands per-environment via Expand. Required.
	HostTemplate string `yaml:"host_template" json:"host_template" validate:"required"`
	// Path is the request path appended to the expanded host. Optional; defaults
	// to "/" when empty.
	Path string `yaml:"path" json:"path"`
	// Criticality is the severity band for this endpoint. Defaults to high when
	// empty (see Normalized). Validated against the closed enum.
	Criticality Criticality `yaml:"criticality" json:"criticality" validate:"omitempty,oneof=critical high medium low"`
	// ExpectedCodes is the set of HTTP status codes considered healthy. When
	// empty, only 200 is accepted (see Accepts). Each code must be a valid HTTP
	// status (100–599).
	ExpectedCodes []int `yaml:"expected_codes" json:"expected_codes" validate:"omitempty,dive,gte=100,lte=599"`
}

// Expand substitutes `{key}` placeholders in HostTemplate from vars and returns
// the full request URL (expanded host + Path). A missing placeholder is left
// verbatim so the error surfaces at request time rather than silently dropping.
// The Path is joined with exactly one separating slash.
func (e Endpoint) Expand(vars map[string]string) string {
	host := e.HostTemplate
	for k, v := range vars {
		host = strings.ReplaceAll(host, "{"+k+"}", v)
	}
	return joinURL(host, e.Path)
}

// URL is Expand with no variables — useful when the host-template has no
// placeholders.
func (e Endpoint) URL() string { return e.Expand(nil) }

// joinURL joins a host and path with exactly one slash, defaulting an empty
// path to "/".
func joinURL(host, path string) string {
	if path == "" {
		path = "/"
	}
	host = strings.TrimRight(host, "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return host + path
}

// Accepts reports whether the given HTTP status code is healthy for this
// endpoint. With no ExpectedCodes configured, only 200 is accepted.
func (e Endpoint) Accepts(code int) bool {
	if len(e.ExpectedCodes) == 0 {
		return code == http.StatusOK
	}
	for _, c := range e.ExpectedCodes {
		if c == code {
			return true
		}
	}
	return false
}

// IsCritical reports whether this endpoint's criticality is the most-severe
// band. An empty criticality is treated as high (not critical).
func (e Endpoint) IsCritical() bool { return e.Criticality == CriticalityCritical }

// Normalized returns a copy with defaults applied: empty Path becomes "/",
// empty Criticality becomes high, empty ExpectedCodes becomes [200]. The
// original is left unchanged. Useful right after Load so downstream code never
// special-cases the empty forms.
func (e Endpoint) Normalized() Endpoint {
	n := e
	if n.Path == "" {
		n.Path = "/"
	}
	if n.Criticality == "" {
		n.Criticality = CriticalityHigh
	}
	if len(n.ExpectedCodes) == 0 {
		n.ExpectedCodes = []int{http.StatusOK}
	}
	return n
}

// EndpointMatrix is an embeddable list of endpoints a tool checks together — a
// reusable replacement for the ad-hoc per-tool endpoint slices the registry
// flagged (D9/D1/D2/D4/D10/C7). It composes with the validate sub-package via a
// `validate:"dive"` tag on the embedding field, and offers typed queries so
// consumers never re-derive criticality filtering or name lookup.
type EndpointMatrix []Endpoint

// ByName returns the endpoint with the given name and whether it was found.
func (m EndpointMatrix) ByName(name string) (Endpoint, bool) {
	for _, e := range m {
		if e.Name == name {
			return e, true
		}
	}
	return Endpoint{}, false
}

// Names returns every endpoint name, in declaration order.
func (m EndpointMatrix) Names() []string {
	names := make([]string, len(m))
	for i, e := range m {
		names[i] = e.Name
	}
	return names
}

// Critical returns only the endpoints in the most-severe criticality band.
func (m EndpointMatrix) Critical() EndpointMatrix {
	return m.WithCriticality(CriticalityCritical)
}

// WithCriticality returns only the endpoints in the given criticality band.
func (m EndpointMatrix) WithCriticality(c Criticality) EndpointMatrix {
	var out EndpointMatrix
	for _, e := range m {
		if e.Criticality == c {
			out = append(out, e)
		}
	}
	return out
}

// Normalized returns a copy with Normalized applied to every endpoint.
func (m EndpointMatrix) Normalized() EndpointMatrix {
	out := make(EndpointMatrix, len(m))
	for i, e := range m {
		out[i] = e.Normalized()
	}
	return out
}

// SortedByCriticality returns a copy ordered most-severe first (critical →
// high → medium → low), stable within a band by declaration order. Useful for
// reporting so the row most likely to matter renders at the top.
func (m EndpointMatrix) SortedByCriticality() EndpointMatrix {
	out := make(EndpointMatrix, len(m))
	copy(out, m)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Criticality.rank() < out[j].Criticality.rank()
	})
	return out
}

// Validate runs the structural invariants the `validate` struct tags cannot
// express on their own: every endpoint must have a non-empty name and host
// template, and names must be unique within the matrix. It is a zero-dependency
// check usable without the validate sub-package; the validate sub-package's
// EndpointMatrix struct-level rule wraps this so both paths share one impl.
func (m EndpointMatrix) Validate() error {
	seen := make(map[string]struct{}, len(m))
	for i, e := range m {
		if e.Name == "" {
			return fmt.Errorf("shikumi: endpoint[%d]: name is required", i)
		}
		if e.HostTemplate == "" {
			return fmt.Errorf("shikumi: endpoint %q: host_template is required", e.Name)
		}
		if _, dup := seen[e.Name]; dup {
			return fmt.Errorf("shikumi: endpoint %q: duplicate name", e.Name)
		}
		seen[e.Name] = struct{}{}
	}
	return nil
}
