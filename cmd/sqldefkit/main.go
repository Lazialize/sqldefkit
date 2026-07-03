// Command sqldefkit bundles a directory tree of .sql schema files into a
// single, dependency-ordered .sql file suitable for feeding to sqldef
// (psqldef/mysqldef/sqlite3def).
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/Lazialize/sqldefkit/internal/bundle"
)

// version is overridable at build time via:
//
//	go build -ldflags "-X main.version=1.2.3"
var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "sqldefkit:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("sqldefkit", flag.ContinueOnError)
	fs.SetOutput(stderr)

	dir := fs.String("dir", ".", "root directory to scan recursively for *.sql files")
	dialectFlag := fs.String("dialect", "", "SQL dialect: postgres, mysql, or sqlite (required)")
	output := fs.String("o", "", "output file path (default: stdout)")
	showVersion := fs.Bool("version", false, "print version and exit")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *showVersion {
		fmt.Fprintln(stdout, version)
		return nil
	}

	dialect, err := parseDialect(*dialectFlag)
	if err != nil {
		return err
	}

	out, err := bundle.Build(*dir, dialect, os.ReadFile)
	if err != nil {
		return err
	}

	if *output == "" {
		_, err = stdout.Write(out)
		return err
	}
	return os.WriteFile(*output, out, 0o644)
}

func parseDialect(s string) (bundle.Dialect, error) {
	switch s {
	case "postgres":
		return bundle.Postgres, nil
	case "mysql":
		return bundle.MySQL, nil
	case "sqlite":
		return bundle.SQLite, nil
	case "":
		return 0, errors.New("--dialect is required (postgres, mysql, or sqlite)")
	default:
		return 0, fmt.Errorf("unknown --dialect %q (expected postgres, mysql, or sqlite)", s)
	}
}
