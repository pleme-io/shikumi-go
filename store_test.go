package shikumi

import (
	"os"
	"path/filepath"
	"testing"
)

type awsBlock struct {
	RegionTarget string `yaml:"regionTarget"`
}

type tcfg struct {
	Tenant string   `yaml:"tenant"`
	Port   int      `yaml:"port"`
	Debug  bool     `yaml:"debug"`
	AWS    awsBlock `yaml:"aws"`
}

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// File overrides defaults; absent fields keep their default; nested keys load.
func TestLoad_FileOverDefaults(t *testing.T) {
	path := writeYAML(t, "tenant: dbk\naws:\n  regionTarget: ap-southeast-1\n")
	def := tcfg{Tenant: "default", Port: 8080}

	cfg, err := Load(path, "", def)
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

// Env (PREFIX_) overrides defaults, with string coercion into int/bool.
func TestLoad_EnvOverDefaults(t *testing.T) {
	t.Setenv("APP_PORT", "9090")
	t.Setenv("APP_DEBUG", "true")
	def := tcfg{Tenant: "default", Port: 8080}

	cfg, err := Load("", "APP_", def)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9090 {
		t.Errorf("port = %d, want 9090 (env, coerced)", cfg.Port)
	}
	if !cfg.Debug {
		t.Errorf("debug = false, want true (env, coerced)")
	}
	if cfg.Tenant != "default" {
		t.Errorf("tenant = %q, want default (kept)", cfg.Tenant)
	}
}

// shikumi precedence: file wins over env on shared keys; env still fills keys
// the file omits.
func TestLoad_FileWinsOverEnv(t *testing.T) {
	path := writeYAML(t, "tenant: dbk\n")
	t.Setenv("APP_TENANT", "envtenant")
	t.Setenv("APP_PORT", "9090")
	def := tcfg{Tenant: "default", Port: 8080}

	cfg, err := Load(path, "APP_", def)
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

// Store loads and exposes config; Reload re-reads the file.
func TestStore_Reload(t *testing.T) {
	path := writeYAML(t, "tenant: one\n")
	store, err := LoadStore(path, "", tcfg{Port: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Get().Tenant; got != "one" {
		t.Fatalf("tenant = %q, want one", got)
	}
	if err := os.WriteFile(path, []byte("tenant: two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := store.Get().Tenant; got != "two" {
		t.Errorf("after reload tenant = %q, want two", got)
	}
}
