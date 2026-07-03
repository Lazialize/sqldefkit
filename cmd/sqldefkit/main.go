// Command sqldefkit bundles a directory tree of .sql schema files into a
// single, dependency-ordered .sql file suitable for feeding to sqldef
// (psqldef/mysqldef/sqlite3def).
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Lazialize/sqldefkit/internal/bundle"
	"github.com/Lazialize/sqldefkit/internal/config"
	"github.com/Lazialize/sqldefkit/internal/diag"
)

// version is overridable at build time via:
//
//	go build -ldflags "-X main.version=1.2.3"
var version = "dev"

const usage = `sqldefkit bundles a directory tree of .sql schema files into a single,
dependency-ordered .sql file for sqldef (psqldef/mysqldef/sqlite3def).

Usage:

	sqldefkit <command> [arguments]

Commands:

	bundle    bundle a directory of .sql files into one file
	check     report diagnostics (errors and warnings) for a schema tree
	version   print version and exit

Use "sqldefkit <command> -h" for details on a specific command.
`

// errCheckFailed is returned by runCheck when at least one error-severity
// diagnostic was found, so main() exits 1. Diagnostics themselves (both
// errors and warnings) are always written to stdout before this is
// returned; the error carries no additional message since main() would
// otherwise print a redundant "sqldefkit: ..." line after the diagnostic
// listing.
var errCheckFailed = errors.New("check found errors")

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err == nil {
		return
	}
	if errors.Is(err, flag.ErrHelp) {
		// Already printed usage to stderr via FlagSet.
		os.Exit(0)
	}
	if errors.Is(err, errCheckFailed) {
		// check already wrote its diagnostics to stdout; nothing more to
		// say, just exit nonzero.
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "sqldefkit:", err)
	os.Exit(1)
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return errors.New("no command given")
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "bundle":
		return runBundle(rest, stdout, stderr)
	case "check":
		return runCheck(rest, stdout, stderr)
	case "version":
		fmt.Fprintln(stdout, version)
		return nil
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		fmt.Fprint(stderr, usage)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func runBundle(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sqldefkit bundle", flag.ContinueOnError)
	fs.SetOutput(stderr)

	configPath := fs.String("config", "", "path to sqldefkit.yaml (default: discovered from the current directory upward)")
	dir := fs.String("dir", "", "root directory to scan recursively for *.sql files (default \".\", or schema_dir from config)")
	dialectFlag := fs.String("dialect", "", "SQL dialect: postgres, mysql, or sqlite (required, from flag or config)")
	output := fs.String("o", "", "output file path (default: stdout, or out from config)")

	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: sqldefkit bundle [--config <path>] [--dir <path>] [--dialect <postgres|mysql|sqlite>] [-o <file>]")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := resolveConfig(*configPath)
	if err != nil {
		return err
	}

	dialect, err := resolveDialect(*dialectFlag, cfg)
	if err != nil {
		return err
	}

	root := resolveDir(*dir, cfg)

	out, err := bundle.Build(root, dialect, os.ReadFile)
	if err != nil {
		return err
	}

	dest := resolveOut(*output, cfg)
	if dest == "" {
		_, err = stdout.Write(out)
		return err
	}
	return os.WriteFile(dest, out, 0o644)
}

// runCheck implements the `check` subcommand: it loads and parses the
// schema tree the same way `bundle` does, but instead of emitting bundled
// SQL, it reports every diagnostic (duplicate definitions, dependency
// cycles, lex/parse failures as errors; unresolved high-confidence
// references as warnings) to stdout, one per line, sorted by
// (file, line, col). It never fails fast: even if error-severity
// diagnostics are present, every diagnostic is still listed. The command
// exits 1 (via errCheckFailed) if any error-severity diagnostic was
// found, 0 otherwise (including when there are warnings but no errors,
// and when there's nothing to report at all).
func runCheck(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sqldefkit check", flag.ContinueOnError)
	fs.SetOutput(stderr)

	configPath := fs.String("config", "", "path to sqldefkit.yaml (default: discovered from the current directory upward)")
	dir := fs.String("dir", "", "root directory to scan recursively for *.sql files (default \".\", or schema_dir from config)")
	dialectFlag := fs.String("dialect", "", "SQL dialect: postgres, mysql, or sqlite (required, from flag or config)")

	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: sqldefkit check [--config <path>] [--dir <path>] [--dialect <postgres|mysql|sqlite>]")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := resolveConfig(*configPath)
	if err != nil {
		return err
	}

	dialect, err := resolveDialect(*dialectFlag, cfg)
	if err != nil {
		return err
	}

	root := resolveDir(*dir, cfg)

	diags, err := bundle.CheckDiagnostics(root, dialect, os.ReadFile)
	if err != nil {
		return err
	}

	for _, d := range diags {
		fmt.Fprintf(stdout, "%s:%d:%d: %s: %s\n", filepath.ToSlash(d.Pos.File), d.Pos.Line, d.Pos.Col, d.Severity, d.Message)
	}

	if diag.HasError(diags) {
		return errCheckFailed
	}
	return nil
}

// resolveConfig loads the config file explicitly named by configPath, or,
// if configPath is empty, discovers one starting from the current
// directory. It is not an error for no config to be found in the
// discovery case (bundle can still work from flags alone); an explicit
// --config that doesn't exist/load IS an error.
func resolveConfig(configPath string) (*config.Config, error) {
	if configPath != "" {
		cfg, err := config.Load(configPath)
		if err != nil {
			return nil, fmt.Errorf("loading --config %s: %w", configPath, err)
		}
		return &cfg, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Discover(cwd)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &cfg, nil
}

// resolveDialect applies flag > config > error precedence.
func resolveDialect(flagVal string, cfg *config.Config) (bundle.Dialect, error) {
	if flagVal != "" {
		return bundle.ParseDialect(flagVal)
	}
	if cfg != nil && cfg.HasDialect {
		return cfg.Dialect, nil
	}
	return 0, errors.New("--dialect is required (postgres, mysql, or sqlite): set it via --dialect or dialect in sqldefkit.yaml")
}

// resolveDir applies flag > config > built-in default (".") precedence.
func resolveDir(flagVal string, cfg *config.Config) string {
	if flagVal != "" {
		return flagVal
	}
	if cfg != nil && cfg.SchemaDir != "" {
		return cfg.SchemaDir
	}
	return "."
}

// resolveOut applies flag > config > built-in default ("", i.e. stdout)
// precedence.
func resolveOut(flagVal string, cfg *config.Config) string {
	if flagVal != "" {
		return flagVal
	}
	if cfg != nil && cfg.Out != "" {
		return cfg.Out
	}
	return ""
}
