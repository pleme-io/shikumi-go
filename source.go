package shikumi

// ConfigSourceKind is the closed set of provider sources a config value can
// originate from, mirroring the Rust `enum ConfigSource {Defaults, Env(String),
// File(PathBuf)}` (source.rs:22-34). Idiomatic Go models the sum as a small
// kind-tag on a comparable struct rather than an interface, so ConfigSource
// stays usable as a map value and comparable for equality.
type ConfigSourceKind uint8

const (
	// SourceDefaults is a machine-derived (bare/discovered/prescribed) value —
	// not operator-supplied.
	SourceDefaults ConfigSourceKind = iota
	// SourceEnv is an environment-variable overlay under the recorded prefix.
	SourceEnv
	// SourceFile is a config-file overlay at the recorded path.
	SourceFile
)

// ConfigSource is the typed provider source that produced a value. It is a
// comparable struct (kind tag + optional Env prefix / File path), the
// idiomatic Go analog of the Rust ConfigSource sum. Only the field relevant to
// Kind is populated; the closed kind set makes a fabricated source
// unrepresentable outside the constructors.
type ConfigSource struct {
	Kind ConfigSourceKind
	Env  string // set iff Kind == SourceEnv
	File string // set iff Kind == SourceFile
}

// DefaultsSource is the machine-derived defaults source (mirror
// ConfigSource::Defaults).
func DefaultsSource() ConfigSource { return ConfigSource{Kind: SourceDefaults} }

// EnvSource is an environment overlay under prefix (mirror ConfigSource::Env).
func EnvSource(prefix string) ConfigSource { return ConfigSource{Kind: SourceEnv, Env: prefix} }

// FileSource is a config-file overlay at path (mirror ConfigSource::File).
func FileSource(path string) ConfigSource { return ConfigSource{Kind: SourceFile, File: path} }

// AsPath returns (path, true) iff this is a File source (mirror
// ConfigSource::as_path, source.rs:38-44).
func (s ConfigSource) AsPath() (string, bool) {
	if s.Kind == SourceFile {
		return s.File, true
	}
	return "", false
}

// AsEnvPrefix returns (prefix, true) iff this is an Env source (mirror
// ConfigSource::as_env_prefix, source.rs:47-53).
func (s ConfigSource) AsEnvPrefix() (string, bool) {
	if s.Kind == SourceEnv {
		return s.Env, true
	}
	return "", false
}

// String renders the source detail through the typed kind (no free-form
// composition of the whole provenance). Defaults renders empty (the tier label
// carries the meaning); Env/File render their coordinate.
func (s ConfigSource) String() string {
	switch s.Kind {
	case SourceEnv:
		return "env: " + s.Env
	case SourceFile:
		return "file: " + s.File
	default:
		return ""
	}
}
