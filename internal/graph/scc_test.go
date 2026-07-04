package graph

import (
	"reflect"
	"testing"
)

func compNames(nodes []Node, comp []int) []string {
	out := make([]string, len(comp))
	for i, idx := range comp {
		out[i] = nodes[idx].Name
	}
	return out
}

func TestSCCs_NoCycleEmpty(t *testing.T) {
	nodes := []Node{
		{File: "a.sql", Index: 0, Name: "orders", Deps: []string{"users"}},
		{File: "b.sql", Index: 0, Name: "users"},
	}
	got := SCCs(nodes)
	if len(got) != 0 {
		t.Errorf("expected no SCCs, got %+v", got)
	}
}

func TestSCCs_SelfLoopNotReported(t *testing.T) {
	nodes := []Node{
		{File: "a.sql", Index: 0, Name: "tree", Deps: []string{"tree"}},
	}
	got := SCCs(nodes)
	if len(got) != 0 {
		t.Errorf("expected no SCCs for self-loop, got %+v", got)
	}
}

func TestSCCs_TwoNodeCycle(t *testing.T) {
	nodes := []Node{
		{File: "a.sql", Index: 0, Name: "a", Deps: []string{"b"}},
		{File: "b.sql", Index: 0, Name: "b", Deps: []string{"a"}},
	}
	got := SCCs(nodes)
	if len(got) != 1 {
		t.Fatalf("expected 1 SCC, got %+v", got)
	}
	want := []string{"a", "b"}
	if !reflect.DeepEqual(compNames(nodes, got[0]), want) {
		t.Errorf("comp = %v, want %v", compNames(nodes, got[0]), want)
	}
}

func TestSCCs_ThreeNodeCycle(t *testing.T) {
	nodes := []Node{
		{File: "a.sql", Index: 0, Name: "a", Deps: []string{"b"}},
		{File: "b.sql", Index: 0, Name: "b", Deps: []string{"c"}},
		{File: "c.sql", Index: 0, Name: "c", Deps: []string{"a"}},
	}
	got := SCCs(nodes)
	if len(got) != 1 {
		t.Fatalf("expected 1 SCC, got %+v", got)
	}
	if len(got[0]) != 3 {
		t.Errorf("expected 3-node SCC, got %+v", compNames(nodes, got[0]))
	}
}

func TestSCCs_MultipleIndependentCycles(t *testing.T) {
	nodes := []Node{
		{File: "a.sql", Index: 0, Name: "a", Deps: []string{"b"}},
		{File: "b.sql", Index: 0, Name: "b", Deps: []string{"a"}},
		{File: "c.sql", Index: 0, Name: "c", Deps: []string{"d"}},
		{File: "d.sql", Index: 0, Name: "d", Deps: []string{"c"}},
		{File: "e.sql", Index: 0, Name: "e"},
	}
	got := SCCs(nodes)
	if len(got) != 2 {
		t.Fatalf("expected 2 SCCs, got %+v", got)
	}
	if !reflect.DeepEqual(compNames(nodes, got[0]), []string{"a", "b"}) {
		t.Errorf("first comp = %v", compNames(nodes, got[0]))
	}
	if !reflect.DeepEqual(compNames(nodes, got[1]), []string{"c", "d"}) {
		t.Errorf("second comp = %v", compNames(nodes, got[1]))
	}
}

func TestSCCs_DeterministicAcrossRuns(t *testing.T) {
	nodes := []Node{
		{File: "z.sql", Index: 0, Name: "z", Deps: []string{"a"}},
		{File: "a.sql", Index: 0, Name: "a", Deps: []string{"m"}},
		{File: "m.sql", Index: 0, Name: "m", Deps: []string{"z"}},
	}
	var prev [][]int
	for i := 0; i < 5; i++ {
		got := SCCs(nodes)
		if prev != nil && !reflect.DeepEqual(prev, got) {
			t.Fatalf("non-deterministic SCCs: %v vs %v", prev, got)
		}
		prev = got
	}
}
