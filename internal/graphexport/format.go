package graphexport

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// FormatJSON renders g as pretty-printed (2-space indent) JSON with a
// trailing newline.
func FormatJSON(g Graph) ([]byte, error) {
	b, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// nodeShape returns the DOT node shape for kind, chosen so the most
// common kinds (table/view/index) are visually distinct at a glance.
func nodeShape(kind string) string {
	switch kind {
	case "table":
		return "box"
	case "view", "materialized_view":
		return "ellipse"
	case "index":
		return "note"
	case "function", "procedure":
		return "component"
	case "trigger":
		return "diamond"
	case "type":
		return "hexagon"
	case "sequence":
		return "cylinder"
	case "extension":
		return "folder"
	case "unknown":
		return "plaintext"
	default:
		return "box"
	}
}

// edgeStyle returns the DOT edge style for an edge kind.
func edgeStyle(kind string) string {
	switch kind {
	case "fk":
		return "solid"
	case "view", "directive":
		return "dashed"
	case "on":
		return "dotted"
	case "alter":
		return "solid"
	default:
		return "solid"
	}
}

// dotQuote quotes s as a DOT identifier/string literal (double-quoted,
// with internal double quotes and backslashes escaped).
func dotQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

// FormatDOT renders g as a Graphviz DOT digraph.
//
//   - Node shape varies by kind (see nodeShape); external nodes are
//     drawn dashed.
//   - Edges inside a cycle are colored red; edge style varies by kind
//     (see edgeStyle).
//   - Identifiers are quoted so schema-qualified names (containing '.')
//     and any other special characters are always valid DOT.
func FormatDOT(g Graph) []byte {
	var b strings.Builder
	b.WriteString("digraph dependencies {\n")
	b.WriteString("  rankdir=LR;\n")

	for _, n := range g.Nodes {
		attrs := []string{
			fmt.Sprintf("label=%s", dotQuote(n.ID)),
			fmt.Sprintf("shape=%s", nodeShape(n.Kind)),
		}
		if n.External {
			attrs = append(attrs, `style="dashed"`)
		}
		if n.InCycle {
			attrs = append(attrs, `color="red"`)
		}
		fmt.Fprintf(&b, "  %s [%s];\n", dotQuote(n.ID), strings.Join(attrs, ", "))
	}

	for _, e := range g.Edges {
		attrs := []string{
			fmt.Sprintf("label=%s", dotQuote(e.Kind)),
			fmt.Sprintf("style=%s", edgeStyle(e.Kind)),
		}
		if e.InCycle {
			attrs = append(attrs, `color="red"`)
		}
		fmt.Fprintf(&b, "  %s -> %s [%s];\n", dotQuote(e.From), dotQuote(e.To), strings.Join(attrs, ", "))
	}

	b.WriteString("}\n")
	return []byte(b.String())
}

// mermaidID sanitizes id into a safe Mermaid flowchart node id: Mermaid
// node ids may not contain '.' (or various other punctuation used in
// schema-qualified SQL names), so this maps any character outside
// [A-Za-z0-9_] to '_' and appends a short content hash to keep the
// mapping collision-resistant (e.g. "a.b" and "a_b" would otherwise both
// sanitize to "a_b"). The human-readable original name is kept as the
// node's label, so nothing is lost visually.
func mermaidID(id string) string {
	var b strings.Builder
	b.WriteString("n_")
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	sum := sha1.Sum([]byte(id))
	b.WriteByte('_')
	b.WriteString(hex.EncodeToString(sum[:])[:8])
	return b.String()
}

// mermaidLabel escapes a label for use inside Mermaid's node-shape
// bracket syntax (e.g. id["label"]): quotes and the shape-delimiting
// bracket characters are escaped/replaced so the label can't break out
// of the node syntax.
func mermaidLabel(s string) string {
	s = strings.ReplaceAll(s, `"`, `#quot;`)
	return s
}

// FormatMermaid renders g as a Mermaid flowchart (graph TD) diagram,
// renderable as-is in a GitHub Markdown ```mermaid code block.
//
//   - Node ids are sanitized (see mermaidID) since Mermaid ids can't
//     contain '.'; the original (possibly schema-qualified) name is kept
//     as the node's visible label.
//   - Cyclic nodes/edges get a distinguishing "cycle" class/style so a
//     cycle stands out visually without changing the graph's shape.
func FormatMermaid(g Graph) []byte {
	var b strings.Builder
	b.WriteString("graph TD\n")

	for _, n := range g.Nodes {
		id := mermaidID(n.ID)
		label := mermaidLabel(n.ID)
		if n.External {
			fmt.Fprintf(&b, "  %s(((%s)))\n", id, label)
		} else {
			fmt.Fprintf(&b, "  %s[%s]\n", id, label)
		}
		if n.InCycle {
			fmt.Fprintf(&b, "  class %s cycle\n", id)
		}
	}

	var cycleLinkStyles []int
	for i, e := range g.Edges {
		from := mermaidID(e.From)
		to := mermaidID(e.To)
		arrow := "-->"
		switch e.Kind {
		case "view", "directive", "on":
			arrow = "-.->"
		}
		fmt.Fprintf(&b, "  %s %s|%s| %s\n", from, arrow, e.Kind, to)
		if e.InCycle {
			cycleLinkStyles = append(cycleLinkStyles, i)
		}
	}

	b.WriteString("  classDef cycle stroke:#ff0000,stroke-width:2px\n")
	for _, i := range cycleLinkStyles {
		fmt.Fprintf(&b, "  linkStyle %d stroke:#ff0000,stroke-width:2px\n", i)
	}

	return []byte(b.String())
}
