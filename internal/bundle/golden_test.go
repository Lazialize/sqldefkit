package bundle

import (
	"os"
	"testing"
)

// TestBuild_Golden bundles the fixture tree under testdata/golden/input
// (tables with cross-file FKs deliberately named so lexicographic order
// doesn't match dependency order, an index, and a view that needs the
// require directive to pick up a subquery-only dependency) and compares
// the result byte-for-byte against testdata/golden/expected.sql. This
// guards against accidental output-format regressions (header, source
// comments, spacing, ordering) beyond what the inline-fixture tests in
// bundle_test.go cover.
//
// To regenerate expected.sql after an intentional output format change:
//
//	go run ./cmd/sqldefkit bundle --dir internal/bundle/testdata/golden/input \
//		--dialect postgres -o internal/bundle/testdata/golden/expected.sql
func TestBuild_Golden(t *testing.T) {
	got, err := Build("testdata/golden/input", Postgres, os.ReadFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want, err := os.ReadFile("testdata/golden/expected.sql")
	if err != nil {
		t.Fatalf("reading golden file: %v", err)
	}

	if string(got) != string(want) {
		t.Errorf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
