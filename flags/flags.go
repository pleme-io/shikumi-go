// Package flags is the dep-bearing flag-layer sub-package for shikumi-go
// (Law 6 — gated to CLI consumers). It wraps a spf13/pflag FlagSet as a shikumi
// Layer via koanf's posflag provider, so CLIs bind flags through the SAME
// precedence chain as env/file rather than parsing flags in parallel. This is
// what makes "did the flag or the file win?" have exactly one fleet answer
// (§2.2): cli-go's Flag[T] reads through this layer's koanf instance.
//
//	fs := pflag.NewFlagSet("app", pflag.ContinueOnError)
//	fs.Int("port", 8080, "listen port")
//	_ = fs.Parse(os.Args[1:])
//	cfg, _ := shikumi.For[Cfg]("app").Layers(flags.Layer(fs)).Load(ctx)
package flags

import (
	"context"

	shikumi "github.com/pleme-io/shikumi-go"

	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	"github.com/spf13/pflag"
)

// flagLayer applies a parsed pflag.FlagSet onto the koanf instance. By koanf's
// posflag semantics, only flags that were actually set on the command line (or
// whose key is absent) participate, so an unset flag does not clobber a
// file/env value — matching the args > env > file precedence when the file is
// applied last but flag-set values are explicit.
type flagLayer struct {
	fs   *pflag.FlagSet
	name string
}

func (l flagLayer) Name() string {
	if l.name != "" {
		return l.name
	}
	return "flags"
}

func (l flagLayer) Apply(_ context.Context, k *koanf.Koanf) error {
	return k.Load(posflag.Provider(l.fs, ".", k), nil)
}

// Layer adapts a (parsed) pflag.FlagSet to a shikumi.Layer. Place it via
// shikumi.Loader.Layers so it sits between defaults and env in the canonical
// pipeline.
func Layer(fs *pflag.FlagSet) shikumi.Layer { return flagLayer{fs: fs} }

// NamedLayer is Layer with a diagnostic label override.
func NamedLayer(fs *pflag.FlagSet, name string) shikumi.Layer {
	return flagLayer{fs: fs, name: name}
}
