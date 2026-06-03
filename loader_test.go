package shikumi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type loaderCfg struct {
	Tenant string             `yaml:"tenant"`
	Port   int                `yaml:"port"`
	Debug  bool               `yaml:"debug"`
	Token  Secret[string]     `yaml:"token"`
	AWS    awsBlock           `yaml:"aws"`
	// Region carries go-envconfig's native env tag with an inline default; the
	// loader's default-applier (go-envconfig, reused not reimplemented) fills it
	// when no layer set it. The yaml key remains "region" for file/env layers.
	Region string `yaml:"region" env:"REGION, default=us-east-1"`
}

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// For[Root] runs the canonical pipeline: defaults seed, file overrides, nested
// keys load, absent fields keep defaults.
func TestFor_FileOverDefaults(t *testing.T) {
	path := writeFile(t, "app.yaml", "tenant: dbk\naws:\n  regionTarget: ap-southeast-1\n")
	cfg, err := For[loaderCfg]("app").
		Path(path).
		Defaults(loaderCfg{Tenant: "default", Port: 8080}).
		Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tenant != "dbk" {
		t.Errorf("tenant = %q, want dbk (file)", cfg.Tenant)
	}
	if cfg.Port != 8080 {
		t.Errorf("port = %d, want 8080 (default kept)", cfg.Port)
	}
	if cfg.AWS.RegionTarget != "ap-southeast-1" {
		t.Errorf("aws.regionTarget = %q, want ap-southeast-1 (nested)", cfg.AWS.RegionTarget)
	}
}

// shikumi precedence through the fluent loader: file wins over env on shared
// keys; env fills file-omitted keys.
func TestFor_FileWinsOverEnv(t *testing.T) {
	path := writeFile(t, "app.yaml", "tenant: dbk\n")
	t.Setenv("APP_TENANT", "envtenant")
	t.Setenv("APP_PORT", "9090")
	cfg, err := For[loaderCfg]("app").
		Path(path).
		EnvPrefix("APP_").
		Defaults(loaderCfg{Tenant: "default", Port: 8080}).
		Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tenant != "dbk" {
		t.Errorf("tenant = %q, want dbk (file wins over env)", cfg.Tenant)
	}
	if cfg.Port != 9090 {
		t.Errorf("port = %d, want 9090 (env fills file-omitted key)", cfg.Port)
	}
}

// `default:` struct tags fill fields untouched by any layer, via go-envconfig's
// applier (reused, not reimplemented).
func TestFor_TagDefaults(t *testing.T) {
	cfg, err := For[loaderCfg]("app").Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("region = %q, want us-east-1 (default: tag applier)", cfg.Region)
	}
}

// A file value overrides the `default:` tag (layer precedence > tag default).
func TestFor_FileBeatsTagDefault(t *testing.T) {
	path := writeFile(t, "app.yaml", "region: eu-west-2\n")
	cfg, err := For[loaderCfg]("app").Path(path).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "eu-west-2" {
		t.Errorf("region = %q, want eu-west-2 (file beats default tag)", cfg.Region)
	}
}

// Secret resolution: a "secret://mem/..." ref is dereferenced by a MemResolver
// at load and lands in a redacting Secret[string].
func TestFor_SecretResolution(t *testing.T) {
	path := writeFile(t, "app.yaml", "token: secret://mem/db-pw\n")
	cfg, err := For[loaderCfg]("app").
		Path(path).
		Secrets(Mem(map[string]string{"db-pw": "hunter2"})).
		Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Token.Expose(); got != "hunter2" {
		t.Errorf("token = %q, want hunter2 (resolved)", got)
	}
	if !cfg.Token.IsSet() {
		t.Error("token IsSet = false, want true")
	}
}

// Default-backend secret refs ("secret://name") route to the first resolver.
func TestFor_SecretDefaultBackend(t *testing.T) {
	path := writeFile(t, "app.yaml", "token: secret://db-pw\n")
	cfg, err := For[loaderCfg]("app").
		Path(path).
		Secrets(Mem(map[string]string{"db-pw": "default-backend"})).
		Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Token.Expose(); got != "default-backend" {
		t.Errorf("token = %q, want default-backend", got)
	}
}

// An unresolved/unknown backend is a hard error (never silently published).
func TestFor_SecretUnknownBackend(t *testing.T) {
	path := writeFile(t, "app.yaml", "token: secret://nope/x\n")
	_, err := For[loaderCfg]("app").
		Path(path).
		Secrets(Mem(map[string]string{})).
		Load(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no resolver") {
		t.Fatalf("err = %v, want 'no resolver' error", err)
	}
}

// EnvResolver dereferences from the process environment.
func TestEnvResolver(t *testing.T) {
	t.Setenv("MY_TOKEN", "from-env")
	path := writeFile(t, "app.yaml", "token: secret://env/MY_TOKEN\n")
	cfg, err := For[loaderCfg]("app").Path(path).Secrets(Env()).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Token.Expose(); got != "from-env" {
		t.Errorf("token = %q, want from-env", got)
	}
}

// Secret[T] redacts under every common surface; only Expose reveals it.
func TestSecret_Redaction(t *testing.T) {
	s := NewSecret("p@ssw0rd")
	for name, got := range map[string]string{
		"String": s.String(),
		"%v":     fmt.Sprintf("%v", s),
		"%s":     fmt.Sprintf("%s", s),
		"%+v":    fmt.Sprintf("%+v", s),
		"%#v":    fmt.Sprintf("%#v", s),
	} {
		if strings.Contains(got, "p@ssw0rd") {
			t.Errorf("%s leaked plaintext: %q", name, got)
		}
		if !strings.Contains(got, "REDACTED") {
			t.Errorf("%s = %q, want REDACTED", name, got)
		}
	}
	b, _ := json.Marshal(struct {
		T Secret[string] `json:"t"`
	}{T: s})
	if strings.Contains(string(b), "p@ssw0rd") {
		t.Errorf("json leaked plaintext: %s", b)
	}
	if s.Expose() != "p@ssw0rd" {
		t.Errorf("Expose = %q, want p@ssw0rd", s.Expose())
	}
	if !IsSecret(s) {
		t.Error("IsSecret(secret) = false, want true")
	}
	if IsSecret("plain") {
		t.Error("IsSecret(string) = true, want false")
	}
}

// Validate runs in Load and rejects an invalid config.
type valCfg struct {
	Tenant string `yaml:"tenant"`
}

func TestFor_ValidateRejects(t *testing.T) {
	v := ValidatorFunc(func(x any) error {
		c := x.(valCfg)
		if c.Tenant == "" {
			return errors.New("tenant required")
		}
		return nil
	})
	path := writeFile(t, "app.yaml", "tenant: \"\"\n")
	_, err := For[valCfg]("app").Path(path).Validate(v).Load(context.Background())
	if err == nil || !strings.Contains(err.Error(), "tenant required") {
		t.Fatalf("err = %v, want validate failure", err)
	}
}

// Two-phase load: phase 1 reads credential-free fields (no secret resolution),
// then phase 2 resolves secrets with a getter built from phase 1.
func TestTwoPhaseLoad(t *testing.T) {
	path := writeFile(t, "app.yaml", "tenant: prod\ntoken: secret://mem/db-pw\n")

	// Phase 1: bootstrap — token ref is NOT resolved (no secrets layer).
	boot, err := For[loaderCfg]("app").Path(path).LoadBootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if boot.Tenant != "prod" {
		t.Fatalf("phase1 tenant = %q, want prod", boot.Tenant)
	}
	// The raw ref survived into the Secret (unresolved) — proving no resolution.
	if got := boot.Token.Expose(); got != "secret://mem/db-pw" {
		t.Fatalf("phase1 token = %q, want raw ref (unresolved)", got)
	}

	// Build the backend from phase-1 fields, then phase 2 resolves.
	getter := map[string]string{"db-pw": "phase2-secret"}
	cfg, err := For[loaderCfg]("app").Path(path).Secrets(Mem(getter)).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Token.Expose(); got != "phase2-secret" {
		t.Errorf("phase2 token = %q, want phase2-secret", got)
	}
}

// LoadStore via the fluent loader threads validation through reloads with
// keep-last-good: a reload that fails validation keeps the prior good value.
func TestLoaderStore_ValidateBeforeSwap_KeepLastGood(t *testing.T) {
	path := writeFile(t, "app.yaml", "tenant: good\n")
	v := ValidatorFunc(func(x any) error {
		if x.(valCfg).Tenant == "bad" {
			return errors.New("tenant cannot be 'bad'")
		}
		return nil
	})
	store, err := For[valCfg]("app").Path(path).Validate(v).LoadStore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Get().Tenant; got != "good" {
		t.Fatalf("initial tenant = %q, want good", got)
	}
	// Write an invalid config and reload — must be rejected, last-good kept.
	if err := os.WriteFile(path, []byte("tenant: bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Reload(); err == nil {
		t.Fatal("Reload of invalid config returned nil, want error")
	}
	if got := store.Get().Tenant; got != "good" {
		t.Errorf("after rejected reload tenant = %q, want good (keep-last-good)", got)
	}
	// A subsequent valid write swaps in cleanly.
	if err := os.WriteFile(path, []byte("tenant: better\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := store.Get().Tenant; got != "better" {
		t.Errorf("after valid reload tenant = %q, want better", got)
	}
}

// JSON files load through the pipeline (format breadth).
func TestFor_JSONFile(t *testing.T) {
	path := writeFile(t, "app.json", `{"tenant":"jsoncfg","port":7000}`)
	cfg, err := For[loaderCfg]("app").Path(path).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tenant != "jsoncfg" || cfg.Port != 7000 {
		t.Errorf("got tenant=%q port=%d, want jsoncfg/7000", cfg.Tenant, cfg.Port)
	}
}

// Functional-option construction mirrors the fluent chain.
func TestFor_FunctionalOptions(t *testing.T) {
	path := writeFile(t, "app.yaml", "tenant: opt\n")
	cfg, err := For[loaderCfg]("app",
		WithDefaults(loaderCfg{Port: 1234}),
	).Path(path).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tenant != "opt" || cfg.Port != 1234 {
		t.Errorf("got tenant=%q port=%d, want opt/1234", cfg.Tenant, cfg.Port)
	}
}

// Back-compat: the legacy Load[T] still works unchanged.
func TestBackCompat_Load(t *testing.T) {
	path := writeFile(t, "app.yaml", "tenant: legacy\n")
	cfg, err := Load(path, "", loaderCfg{Port: 5})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tenant != "legacy" || cfg.Port != 5 {
		t.Errorf("legacy Load got tenant=%q port=%d", cfg.Tenant, cfg.Port)
	}
}
