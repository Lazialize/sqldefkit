package graphexport

import (
	"strings"

	"github.com/Lazialize/sqldefkit/internal/fkrewrite"
	"github.com/Lazialize/sqldefkit/internal/lexer"
	"github.com/Lazialize/sqldefkit/internal/parse"
)

// exportColumns converts fkrewrite.Column values (internal extraction
// shape, shared with the FK-cycle-splitting machinery) to the payload's
// own Column type. Returns nil for an empty/nil input so Node.Columns
// stays omitted (omitempty) rather than becoming an empty array.
func exportColumns(cols []fkrewrite.Column) []Column {
	if len(cols) == 0 {
		return nil
	}
	out := make([]Column, len(cols))
	for i, c := range cols {
		out[i] = Column{
			Name:    c.Name,
			Type:    c.Type,
			PK:      c.PK,
			NotNull: c.NotNull,
			Unique:  c.Unique,
		}
		if c.FK != nil {
			out[i].FK = &ColumnFK{Table: c.FK.Table, Column: c.FK.Column}
		}
	}
	return out
}

// fkColumnPair is one (source column, target column) pairing for a single
// REFERENCES occurrence found while scanning a CREATE TABLE's column list.
type fkColumnPair struct {
	from, to string
}

// fkColumnPairs finds, for a CREATE TABLE statement, one fkColumnPair per
// FK clause occurrence (inline or table-level), grouped by target table,
// in source (left-to-right) order. It combines two existing extractors
// rather than a third column-list walk:
//
//   - fkrewrite.Extract (built for FK-cycle splitting) already finds
//     exactly one Clause per REFERENCES occurrence, inline or
//     table-level, in source item order — the same order
//     parse.scanReferences finds the same clauses in, so Build can pair
//     these positionally with the "fk"-classified parse.Ref entries for
//     the same target in DepRefs order (see classifyRefs).
//   - fkrewrite.ExtractColumns resolves each occurrence's specific
//     from/to column names (Clause carries the target table but not the
//     target column).
//
// For an inline clause, Clause.Column already names the source column
// directly. For a table-level clause (single or composite), the source
// column(s) aren't on Clause at all, so this looks up the first
// ExtractColumns entry whose FK matches this clause's target and hasn't
// been claimed by an earlier clause targeting the same table yet —
// correct as long as clauses targeting the same table are matched to
// ExtractColumns entries in the same left-to-right order, which holds
// since both walk the same column/constraint list.
func fkColumnPairs(stmtKind parse.Kind, text string, tokens []lexer.Token) map[string][]fkColumnPair {
	if stmtKind != parse.KindCreateTable {
		return nil
	}
	clauses, ok := fkrewrite.Extract(text, tokens)
	if !ok || len(clauses) == 0 {
		return nil
	}
	cols := fkrewrite.ExtractColumns(text, tokens)

	// colsByTarget queues, per target table, every ExtractColumns entry
	// with an FK pointing there, in ExtractColumns' own order (column
	// definitions first, then table-level constraints — see its doc
	// comment). Each clause below consumes entries from its target's
	// queue: an inline clause consumes the entry matching its own column
	// by name (it names its column directly via Clause.Column); a
	// table-level clause consumes the next not-yet-consumed entry for its
	// target, since composite table-level FKs aren't otherwise
	// distinguishable from Column alone.
	colsByTarget := make(map[string][]fkrewrite.Column)
	for _, c := range cols {
		if c.FK != nil {
			colsByTarget[c.FK.Table] = append(colsByTarget[c.FK.Table], c)
		}
	}

	out := make(map[string][]fkColumnPair)
	for _, cl := range clauses {
		list := colsByTarget[cl.Target]
		var fromCol, toCol string
		consumeAt := -1
		if !cl.TableLevel {
			for i, c := range list {
				if strings.EqualFold(c.Name, cl.Column) {
					consumeAt = i
					break
				}
			}
		} else if len(list) > 0 {
			consumeAt = 0
		}
		if consumeAt >= 0 {
			fromCol = list[consumeAt].Name
			toCol = list[consumeAt].FK.Column
			colsByTarget[cl.Target] = append(list[:consumeAt], list[consumeAt+1:]...)
		}
		out[cl.Target] = append(out[cl.Target], fkColumnPair{from: fromCol, to: toCol})
	}
	return out
}
