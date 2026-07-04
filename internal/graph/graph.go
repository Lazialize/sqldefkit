// Package graph performs a deterministic topological sort over bundle
// statements based on their defined/depended-on object names.
package graph

import (
	"container/heap"
	"fmt"
	"strings"
)

// Node is one sortable unit: a statement defining (optionally) a named
// object, depending on zero or more named objects. File, Index, and
// SubIndex together provide the deterministic tie-break key and are also
// used to render cycle error messages as "file:name".
type Node struct {
	File  string // relative source file path
	Index int    // statement index within File (0-based, source order)
	// SubIndex breaks ties among multiple nodes sharing the same (File,
	// Index): every real, source-parsed statement leaves this at the
	// zero value, so existing behavior/output is unaffected; it's used
	// only by synthesized nodes (e.g. an ALTER TABLE split out of a
	// CREATE TABLE to break a foreign-key dependency cycle), which sort
	// immediately after the real statement they share (File, Index)
	// with, ordered among themselves by SubIndex.
	SubIndex int
	Name     string // defined object name, normalized; "" if none
	Deps     []string
}

// Sort performs a stable topological sort of nodes using Kahn's
// algorithm. Ties among ready nodes are broken by (File, Index) so output
// is deterministic and follows natural file order wherever dependencies
// allow. Dependencies naming objects not defined by any node are ignored
// for ordering purposes.
//
// Returns the nodes in dependency-respecting order, or an error
// describing a cycle if one exists.
func Sort(nodes []Node) ([]Node, error) {
	// Map defined object name -> node index. Duplicate definitions are
	// assumed already validated by the caller (bundle package); if
	// present here, the last one wins for edge resolution.
	byName := make(map[string]int, len(nodes))
	for i, n := range nodes {
		if n.Name != "" {
			byName[n.Name] = i
		}
	}

	// indegree[i] = number of edges pointing into node i, i.e. number of
	// distinct dependencies of node i that resolve to a defined node.
	indegree := make([]int, len(nodes))
	// dependents[i] = indices of nodes that depend on node i.
	dependents := make([][]int, len(nodes))

	for i, n := range nodes {
		seen := make(map[int]bool)
		for _, dep := range n.Deps {
			j, ok := byName[dep]
			if !ok || j == i {
				continue // pre-existing/external dependency, or self-loop
			}
			if seen[j] {
				continue
			}
			seen[j] = true
			indegree[i]++
			dependents[j] = append(dependents[j], i)
		}
	}

	pq := &readyQueue{}
	heap.Init(pq)
	for i, n := range nodes {
		if indegree[i] == 0 {
			heap.Push(pq, ready{file: n.File, index: n.Index, subIndex: n.SubIndex, nodeIdx: i})
		}
	}

	order := make([]Node, 0, len(nodes))
	visited := make([]bool, len(nodes))

	for pq.Len() > 0 {
		r := heap.Pop(pq).(ready)
		i := r.nodeIdx
		visited[i] = true
		order = append(order, nodes[i])

		// Sort dependents by (File, Index) before pushing so that push
		// order doesn't affect the heap's tie-break stability (heap
		// already sorts, but iterate deterministically regardless).
		deps := append([]int(nil), dependents[i]...)
		for _, j := range deps {
			indegree[j]--
			if indegree[j] == 0 {
				heap.Push(pq, ready{file: nodes[j].File, index: nodes[j].Index, subIndex: nodes[j].SubIndex, nodeIdx: j})
			}
		}
	}

	if len(order) < len(nodes) {
		path := findCyclePath(nodes, visited)
		return nil, &CycleError{Nodes: path}
	}

	return order, nil
}

// CycleError is returned by Sort when the graph contains a dependency
// cycle. Nodes holds the cycle path in the same deterministic order
// rendered by Error() (starting at the smallest (File, Index) among the
// unresolved nodes, repeating that starting node at the end to close the
// loop).
type CycleError struct {
	Nodes []Node
	// Note, if non-empty, is appended to Error() as an extra sentence.
	// Used by internal/bundle's FK-cycle-breaking pass to explain why a
	// cycle made entirely of foreign keys still couldn't be split
	// automatically, without changing the base message for cycles that
	// were never FK-only in the first place (Note left empty for those).
	Note string
}

func (e *CycleError) Error() string {
	parts := make([]string, len(e.Nodes))
	for i, n := range e.Nodes {
		parts[i] = fmt.Sprintf("%s:%s", n.File, n.Name)
	}
	msg := fmt.Sprintf("dependency cycle detected: %s", strings.Join(parts, " -> "))
	if e.Note != "" {
		msg += " (" + e.Note + ")"
	}
	return msg
}

type ready struct {
	file     string
	index    int
	subIndex int
	nodeIdx  int
}

// readyQueue is a min-heap of ready nodes ordered by (file, index, subIndex).
type readyQueue []ready

func (q readyQueue) Len() int { return len(q) }
func (q readyQueue) Less(i, j int) bool {
	if q[i].file != q[j].file {
		return q[i].file < q[j].file
	}
	if q[i].index != q[j].index {
		return q[i].index < q[j].index
	}
	return q[i].subIndex < q[j].subIndex
}
func (q readyQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *readyQueue) Push(x any)   { *q = append(*q, x.(ready)) }
func (q *readyQueue) Pop() any {
	old := *q
	n := len(old)
	item := old[n-1]
	*q = old[:n-1]
	return item
}

// findCyclePath locates one cycle among the not-yet-visited nodes (those
// with remaining indegree > 0) and returns it as a slice of Nodes in
// dependency order (repeating the starting node at the end to close the
// loop), starting deterministically from the smallest (File, Index)
// among the unresolved nodes.
func findCyclePath(nodes []Node, visited []bool) []Node {
	remaining := make(map[int]bool)
	for i := range nodes {
		if !visited[i] {
			remaining[i] = true
		}
	}
	if len(remaining) == 0 {
		return nil
	}

	// Rebuild forward edges (i -> j means i depends on j, i.e. j must
	// come first) restricted to remaining nodes, to walk a path.
	byName := make(map[string]int, len(nodes))
	for i, n := range nodes {
		if n.Name != "" {
			byName[n.Name] = i
		}
	}
	edges := make(map[int][]int)
	for i := range remaining {
		for _, dep := range nodes[i].Deps {
			j, ok := byName[dep]
			if !ok || j == i || !remaining[j] {
				continue
			}
			edges[i] = append(edges[i], j)
		}
	}

	// Deterministically pick a starting node: smallest (File, Index)
	// among remaining.
	var start int
	first := true
	for i := range remaining {
		if first || less(nodes[i], nodes[start]) {
			start = i
			first = false
		}
	}

	visitedPath := make(map[int]int) // node -> position in path
	path := []int{start}
	visitedPath[start] = 0
	cur := start
	for {
		nexts := edges[cur]
		if len(nexts) == 0 {
			break
		}
		// deterministic: smallest (File, Index) among next hops
		next := nexts[0]
		for _, n := range nexts[1:] {
			if less(nodes[n], nodes[next]) {
				next = n
			}
		}
		if pos, ok := visitedPath[next]; ok {
			path = append(path, next)
			path = path[pos:]
			break
		}
		visitedPath[next] = len(path)
		path = append(path, next)
		cur = next
	}

	result := make([]Node, len(path))
	for i, idx := range path {
		result[i] = nodes[idx]
	}
	return result
}

func less(a, b Node) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	if a.Index != b.Index {
		return a.Index < b.Index
	}
	return a.SubIndex < b.SubIndex
}
