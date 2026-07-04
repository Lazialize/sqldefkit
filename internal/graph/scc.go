package graph

import "sort"

// SCCs computes the strongly connected components of nodes' dependency
// graph, using the same edge-resolution rules as Sort (a dependency name
// not defined by any node is ignored; a self-dependency is dropped and
// never contributes an edge). Only components with more than one member
// are returned — a single node with no self-loop trivially can't be part
// of a cycle, and self-loops are deliberately not edges here (see Sort's
// doc comment), so a lone node is never reported even if it names itself
// as a dependency.
//
// Each returned component is a slice of indices into nodes, sorted by
// (File, Index) for determinism. The components themselves are also
// sorted by their smallest member's (File, Index), so callers get a
// stable iteration order run to run.
func SCCs(nodes []Node) [][]int {
	byName := make(map[string]int, len(nodes))
	for i, n := range nodes {
		if n.Name != "" {
			byName[n.Name] = i
		}
	}

	// adj[i] = distinct node indices that i depends on (edges i -> j
	// meaning j must be available before i, same direction Sort uses).
	adj := make([][]int, len(nodes))
	for i, n := range nodes {
		seen := make(map[int]bool)
		for _, dep := range n.Deps {
			j, ok := byName[dep]
			if !ok || j == i || seen[j] {
				continue
			}
			seen[j] = true
			adj[i] = append(adj[i], j)
		}
	}

	t := &tarjan{
		adj:     adj,
		index:   make([]int, len(nodes)),
		low:     make([]int, len(nodes)),
		onStack: make([]bool, len(nodes)),
		visited: make([]bool, len(nodes)),
	}
	for i := range nodes {
		t.index[i] = -1
	}

	// Iterate deterministically by (File, Index) so the DFS order (and
	// hence which node ends up as an SCC's "root") doesn't depend on
	// input slice order.
	order := make([]int, len(nodes))
	for i := range nodes {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return less(nodes[order[a]], nodes[order[b]]) })

	for _, i := range order {
		if !t.visited[i] {
			t.strongConnect(nodes, i)
		}
	}

	// Keep only components with >1 member.
	var out [][]int
	for _, comp := range t.components {
		if len(comp) > 1 {
			sort.Slice(comp, func(a, b int) bool { return less(nodes[comp[a]], nodes[comp[b]]) })
			out = append(out, comp)
		}
	}
	sort.Slice(out, func(a, b int) bool { return less(nodes[out[a][0]], nodes[out[b][0]]) })
	return out
}

// tarjan holds the working state for an iterative-by-recursion (Go's
// default stack is generous enough for schema-sized graphs) Tarjan SCC
// computation. Neighbors within each node's adjacency list are also
// visited in deterministic (File, Index) order so the resulting
// components (though a set) are built via a reproducible traversal.
type tarjan struct {
	adj        [][]int
	index      []int
	low        []int
	onStack    []bool
	visited    []bool
	stack      []int
	counter    int
	components [][]int
}

func (t *tarjan) strongConnect(nodes []Node, v int) {
	t.visited[v] = true
	t.index[v] = t.counter
	t.low[v] = t.counter
	t.counter++
	t.stack = append(t.stack, v)
	t.onStack[v] = true

	neighbors := append([]int(nil), t.adj[v]...)
	sort.Slice(neighbors, func(a, b int) bool { return less(nodes[neighbors[a]], nodes[neighbors[b]]) })

	for _, w := range neighbors {
		if !t.visited[w] {
			t.strongConnect(nodes, w)
			if t.low[w] < t.low[v] {
				t.low[v] = t.low[w]
			}
		} else if t.onStack[w] {
			if t.index[w] < t.low[v] {
				t.low[v] = t.index[w]
			}
		}
	}

	if t.low[v] == t.index[v] {
		var comp []int
		for {
			n := len(t.stack) - 1
			w := t.stack[n]
			t.stack = t.stack[:n]
			t.onStack[w] = false
			comp = append(comp, w)
			if w == v {
				break
			}
		}
		t.components = append(t.components, comp)
	}
}
