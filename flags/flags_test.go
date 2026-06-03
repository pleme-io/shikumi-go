package flags_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	shikumi "github.com/pleme-io/shikumi-go"
	"github.com/pleme-io/shikumi-go/flags"

	"github.com/spf13/pflag"
)

type cfg struct {
	Tenant string `yaml:"tenant"`
	Port   int    `yaml:"port"`
}

// A flag set value flows through the same pipeline; an explicitly-set flag wins
// over the default, and the file (applied last) wins over an unset flag's
// default — proving the flag layer participates in one chain.
func TestFlagLayer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(path, []byte("tenant: filetenant\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fs := pflag.NewFlagSet("app", pflag.ContinueOnError)
	fs.Int("port", 0, "listen port")
	fs.String("tenant", "", "tenant")
	if err := fs.Parse([]string{"--port", "9999"}); err != nil {
		t.Fatal(err)
	}

	c, err := shikumi.For[cfg]("app").
		Path(path).
		Defaults(cfg{Port: 1, Tenant: "default"}).
		Layers(flags.Layer(fs)).
		Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 9999 {
		t.Errorf("port = %d, want 9999 (explicit flag)", c.Port)
	}
	if c.Tenant != "filetenant" {
		t.Errorf("tenant = %q, want filetenant (file over unset flag)", c.Tenant)
	}
}
