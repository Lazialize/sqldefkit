package graph

import (
	"strings"
	"testing"
)

func names(nodes []Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Name
	}
	return out
}

func TestSort_OrdersByDependency(t *testing.T) {
	nodes := []Node{
		{File: "a.sql", Index: 0, Name: "orders", Deps: []string{"users"}},
		{File: "b.sql", Index: 0, Name: "users"},
	}
	got, err := Sort(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"users", "orders"}
	if strings.Join(names(got), ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", names(got), want)
	}
}

func TestSort_DeterministicTieBreakByFileThenIndex(t *testing.T) {
	// No dependencies among these three; natural order should be by
	// (File, Index) regardless of input slice order.
	nodes := []Node{
		{File: "b.sql", Index: 0, Name: "b_obj"},
		{File: "a.sql", Index: 1, Name: "a_obj_1"},
		{File: "a.sql", Index: 0, Name: "a_obj_0"},
	}
	got, err := Sort(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"a_obj_0", "a_obj_1", "b_obj"}
	if strings.Join(names(got), ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", names(got), want)
	}
}

func TestSort_StableAcrossMultipleRuns(t *testing.T) {
	nodes := []Node{
		{File: "z.sql", Index: 0, Name: "z", Deps: []string{"a"}},
		{File: "a.sql", Index: 0, Name: "a"},
		{File: "m.sql", Index: 0, Name: "m", Deps: []string{"a"}},
	}
	var prev []string
	for i := 0; i < 5; i++ {
		got, err := Sort(nodes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cur := names(got)
		if prev != nil && strings.Join(prev, ",") != strings.Join(cur, ",") {
			t.Fatalf("non-deterministic order: %v vs %v", prev, cur)
		}
		prev = cur
	}
	want := []string{"a", "m", "z"}
	if strings.Join(prev, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", prev, want)
	}
}

func TestSort_IgnoresExternalDependencies(t *testing.T) {
	nodes := []Node{
		{File: "a.sql", Index: 0, Name: "orders", Deps: []string{"users_not_in_bundle"}},
	}
	got, err := Sort(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "orders" {
		t.Errorf("got = %+v", got)
	}
}

func TestSort_CycleDetected(t *testing.T) {
	nodes := []Node{
		{File: "a.sql", Index: 0, Name: "users", Deps: []string{"orders"}},
		{File: "b.sql", Index: 0, Name: "orders", Deps: []string{"users"}},
	}
	_, err := Sort(nodes)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "a.sql:users") || !strings.Contains(msg, "b.sql:orders") {
		t.Errorf("cycle message = %q, missing expected node references", msg)
	}
	if !strings.Contains(msg, "->") {
		t.Errorf("cycle message = %q, expected arrow separators", msg)
	}
}

func TestSort_SelfLoopIgnored(t *testing.T) {
	// A node depending on itself (e.g. via directive by mistake) should
	// not be treated as a cycle; self-deps are dropped during edge
	// construction.
	nodes := []Node{
		{File: "a.sql", Index: 0, Name: "tree", Deps: []string{"tree"}},
	}
	got, err := Sort(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got = %+v", got)
	}
}
