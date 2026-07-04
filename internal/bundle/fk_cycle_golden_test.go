package bundle

import (
	"os"
	"testing"
)

// TestBuild_FKCycleGolden pins the exact rewritten output for a two-table
// mutual foreign-key cycle (both inline REFERENCES): the golden case
// called out in the task spec. To regenerate after an intentional output
// format change:
//
//	go run ./cmd/sqldefkit bundle --dir internal/bundle/testdata/fkcycle/input \
//		--dialect postgres -o internal/bundle/testdata/fkcycle/expected.sql
func TestBuild_FKCycleGolden(t *testing.T) {
	got, err := Build("testdata/fkcycle/input", Postgres, os.ReadFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want, err := os.ReadFile("testdata/fkcycle/expected.sql")
	if err != nil {
		t.Fatalf("reading golden file: %v", err)
	}

	if string(got) != string(want) {
		t.Errorf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
