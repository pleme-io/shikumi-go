package shikumi

import (
	"testing"
)

func TestEndpointExpand(t *testing.T) {
	tests := []struct {
		name string
		ep   Endpoint
		vars map[string]string
		want string
	}{
		{
			name: "host template with var and path",
			ep:   Endpoint{HostTemplate: "https://{region}.gw.example.com", Path: "/v2/health"},
			vars: map[string]string{"region": "us-east-1"},
			want: "https://us-east-1.gw.example.com/v2/health",
		},
		{
			name: "empty path defaults to slash",
			ep:   Endpoint{HostTemplate: "https://gw.example.com"},
			want: "https://gw.example.com/",
		},
		{
			name: "trailing slash on host is normalized",
			ep:   Endpoint{HostTemplate: "https://gw.example.com/", Path: "/v2"},
			want: "https://gw.example.com/v2",
		},
		{
			name: "path without leading slash",
			ep:   Endpoint{HostTemplate: "https://gw.example.com", Path: "v2/health"},
			want: "https://gw.example.com/v2/health",
		},
		{
			name: "unknown placeholder left verbatim",
			ep:   Endpoint{HostTemplate: "https://{region}.gw.example.com", Path: "/x"},
			want: "https://{region}.gw.example.com/x",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ep.Expand(tt.vars); got != tt.want {
				t.Errorf("Expand() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEndpointAccepts(t *testing.T) {
	tests := []struct {
		name  string
		codes []int
		code  int
		want  bool
	}{
		{name: "default accepts 200", codes: nil, code: 200, want: true},
		{name: "default rejects 204", codes: nil, code: 204, want: false},
		{name: "explicit set accepts member", codes: []int{200, 204}, code: 204, want: true},
		{name: "explicit set rejects non-member", codes: []int{200, 204}, code: 500, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep := Endpoint{ExpectedCodes: tt.codes}
			if got := ep.Accepts(tt.code); got != tt.want {
				t.Errorf("Accepts(%d) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}

func TestEndpointNormalized(t *testing.T) {
	got := Endpoint{Name: "gw", HostTemplate: "https://gw"}.Normalized()
	if got.Path != "/" {
		t.Errorf("Path = %q, want /", got.Path)
	}
	if got.Criticality != CriticalityHigh {
		t.Errorf("Criticality = %q, want high", got.Criticality)
	}
	if len(got.ExpectedCodes) != 1 || got.ExpectedCodes[0] != 200 {
		t.Errorf("ExpectedCodes = %v, want [200]", got.ExpectedCodes)
	}
}

func TestCriticalityIsCritical(t *testing.T) {
	tests := []struct {
		c    Criticality
		want bool
	}{
		{CriticalityCritical, true},
		{CriticalityHigh, false},
		{CriticalityMedium, false},
		{CriticalityLow, false},
		{Criticality(""), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.c), func(t *testing.T) {
			if got := tt.c.IsCritical(); got != tt.want {
				t.Errorf("IsCritical() = %v, want %v", got, tt.want)
			}
		})
	}
}

func sampleMatrix() EndpointMatrix {
	return EndpointMatrix{
		{Name: "gateway", HostTemplate: "https://gw", Criticality: CriticalityCritical},
		{Name: "metrics", HostTemplate: "https://mx", Criticality: CriticalityLow},
		{Name: "auth", HostTemplate: "https://au", Criticality: CriticalityHigh},
	}
}

func TestEndpointMatrixQueries(t *testing.T) {
	m := sampleMatrix()

	if got := m.Names(); len(got) != 3 || got[0] != "gateway" {
		t.Errorf("Names() = %v", got)
	}

	if e, ok := m.ByName("auth"); !ok || e.HostTemplate != "https://au" {
		t.Errorf("ByName(auth) = %+v, %v", e, ok)
	}
	if _, ok := m.ByName("nope"); ok {
		t.Errorf("ByName(nope) should not be found")
	}

	crit := m.Critical()
	if len(crit) != 1 || crit[0].Name != "gateway" {
		t.Errorf("Critical() = %+v", crit)
	}

	low := m.WithCriticality(CriticalityLow)
	if len(low) != 1 || low[0].Name != "metrics" {
		t.Errorf("WithCriticality(low) = %+v", low)
	}
}

func TestEndpointMatrixSortedByCriticality(t *testing.T) {
	got := sampleMatrix().SortedByCriticality()
	wantOrder := []string{"gateway", "auth", "metrics"} // critical, high, low
	for i, name := range wantOrder {
		if got[i].Name != name {
			t.Errorf("SortedByCriticality()[%d] = %q, want %q", i, got[i].Name, name)
		}
	}
	// ensure original is not mutated
	if sampleMatrix()[0].Name != "gateway" {
		t.Errorf("sort mutated the original")
	}
}

func TestEndpointMatrixValidate(t *testing.T) {
	tests := []struct {
		name    string
		m       EndpointMatrix
		wantErr bool
	}{
		{
			name:    "valid",
			m:       sampleMatrix(),
			wantErr: false,
		},
		{
			name:    "missing name",
			m:       EndpointMatrix{{HostTemplate: "https://x"}},
			wantErr: true,
		},
		{
			name:    "missing host template",
			m:       EndpointMatrix{{Name: "x"}},
			wantErr: true,
		},
		{
			name: "duplicate name",
			m: EndpointMatrix{
				{Name: "dup", HostTemplate: "https://a"},
				{Name: "dup", HostTemplate: "https://b"},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.m.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
