// Package config loads sqldefkit.yaml, the project config file. Its
// presence also marks a project root: a future LSP server will walk up
// from an open .sql file looking for this file to find the project root
// and dialect.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/Lazialize/sqldefkit/internal/bundle"
)

// FileNames are the recognized config file names, in the directory
// they're looked for. Both are accepted, but having both in the same
// directory is an error (see Discover and Load).
var FileNames = []string{"sqldefkit.yaml", "sqldefkit.yml"}

// ErrNotFound is returned (wrapped) by Discover when no config file is
// found between startDir and the filesystem root.
var ErrNotFound = errors.New("sqldefkit.yaml not found")

// Config is the parsed, resolved contents of a sqldefkit.yaml file.
// SchemaDir and Out are resolved to absolute paths at load time, relative
// to the directory the config file was found in.
type Config struct {
	// Dialect is the SQL dialect, if set in the file.
	Dialect bundle.Dialect
	// HasDialect reports whether Dialect was set in the file (bundle.Dialect
	// has no natural zero value distinct from postgres).
	HasDialect bool

	// SchemaDir is the resolved path to the schema root. Empty if not set
	// in the file (caller should apply its own default).
	SchemaDir string

	// Out is the resolved default output path. Empty if not set in the
	// file (caller should default to stdout).
	Out string

	// Dir is the directory the config file was found/loaded from.
	Dir string

	// Path is the full path to the config file itself.
	Path string
}

// rawConfig mirrors the on-disk YAML schema exactly, for strict decoding.
type rawConfig struct {
	Dialect   string `yaml:"dialect"`
	SchemaDir string `yaml:"schema_dir"`
	Out       string `yaml:"out"`
}

// Discover walks upward from startDir looking for a config file, stopping
// at the first directory that contains one. It returns ErrNotFound
// (wrapped, check with errors.Is) if none is found before reaching the
// filesystem root.
func Discover(startDir string) (Config, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return Config{}, err
	}

	for {
		path, err := findConfigFile(dir)
		if err != nil {
			return Config{}, err
		}
		if path != "" {
			return Load(path)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return Config{}, fmt.Errorf("%w (searched from %s upward)", ErrNotFound, startDir)
		}
		dir = parent
	}
}

// findConfigFile checks dir for sqldefkit.yaml/.yml, returning the full
// path to the one found, "" if neither exists, or an error if both exist.
func findConfigFile(dir string) (string, error) {
	var found []string
	for _, name := range FileNames {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			found = append(found, p)
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	switch len(found) {
	case 0:
		return "", nil
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("both sqldefkit.yaml and sqldefkit.yml found in %s: keep only one", dir)
	}
}

// Load reads and strictly decodes the config file at path, resolving
// schema_dir and out relative to path's directory.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var raw rawConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("parsing %s: %w", path, err)
	}

	dir := filepath.Dir(path)
	cfg := Config{
		Dir:  dir,
		Path: path,
	}

	if raw.Dialect != "" {
		d, err := bundle.ParseDialect(raw.Dialect)
		if err != nil {
			return Config{}, fmt.Errorf("parsing %s: %w", path, err)
		}
		cfg.Dialect = d
		cfg.HasDialect = true
	}

	if raw.SchemaDir != "" {
		cfg.SchemaDir = resolvePath(dir, raw.SchemaDir)
	}

	if raw.Out != "" {
		cfg.Out = resolvePath(dir, raw.Out)
	}

	return cfg, nil
}

// resolvePath returns p unchanged if it's already absolute, otherwise
// joins it onto base.
func resolvePath(base, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}
