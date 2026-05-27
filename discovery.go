package shikumi

import (
	"os"
	"path/filepath"
)

// Format is a supported config file format.
type Format int

const (
	Yaml Format = iota // .yaml
	Yml                // .yml
	Toml               // .toml
)

// Ext returns the file extension (without dot) for the format.
func (f Format) Ext() string {
	switch f {
	case Yml:
		return "yml"
	case Toml:
		return "toml"
	default:
		return "yaml"
	}
}

// DefaultFormats is the preference order used when none is set.
var DefaultFormats = []Format{Yaml, Yml, Toml}

// Discovery locates a config file for an app using the shikumi precedence:
//
//  1. env-var path override (e.g. $REBUILD_DB_RO_CONFIG), if set,
//  2. $XDG_CONFIG_HOME/{app}/{app}.{ext},
//  3. $HOME/.config/{app}/{app}.{ext},
//  4. any extra dirs (e.g. /etc/{app} or a repo-local dir),
//  5. legacy dotfiles: $HOME/.{app}, $HOME/.{app}.{ext}.
//
// Within a directory, formats are tried in preference order.
type Discovery struct {
	app         string
	envOverride string
	formats     []Format
	extraDirs   []string
}

// New starts a Discovery for the named app.
func New(app string) *Discovery {
	return &Discovery{app: app, formats: DefaultFormats}
}

// EnvOverride sets the env var whose value, if present, is the exact config path.
func (d *Discovery) EnvOverride(name string) *Discovery { d.envOverride = name; return d }

// Formats sets the format preference order.
func (d *Discovery) Formats(fs ...Format) *Discovery {
	if len(fs) > 0 {
		d.formats = fs
	}
	return d
}

// Dirs appends extra search directories (tried after the XDG/home locations,
// before the legacy dotfiles).
func (d *Discovery) Dirs(dirs ...string) *Discovery {
	d.extraDirs = append(d.extraDirs, dirs...)
	return d
}

// candidates returns ordered candidate paths, highest precedence first.
func (d *Discovery) candidates() []string {
	var paths []string

	if d.envOverride != "" {
		if p := os.Getenv(d.envOverride); p != "" {
			paths = append(paths, p)
		}
	}

	formatted := func(dir string) {
		for _, f := range d.formats {
			paths = append(paths, filepath.Join(dir, d.app+"."+f.Ext()))
		}
	}

	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		formatted(filepath.Join(xdg, d.app))
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		formatted(filepath.Join(home, ".config", d.app))
	}
	for _, dir := range d.extraDirs {
		formatted(dir)
	}
	if home != "" {
		paths = append(paths, filepath.Join(home, "."+d.app))
		for _, f := range d.formats {
			paths = append(paths, filepath.Join(home, "."+d.app+"."+f.Ext()))
		}
	}
	return paths
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Discover returns the first existing config file, or ErrNotFound.
func (d *Discovery) Discover() (string, error) {
	for _, p := range d.candidates() {
		if isFile(p) {
			return p, nil
		}
	}
	return "", ErrNotFound
}

// DiscoverAll returns every existing config file across the hierarchy, highest
// precedence first, de-duplicated.
func (d *Discovery) DiscoverAll() []string {
	var found []string
	seen := map[string]bool{}
	for _, p := range d.candidates() {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if isFile(abs) && !seen[abs] {
			found = append(found, abs)
			seen[abs] = true
		}
	}
	return found
}
