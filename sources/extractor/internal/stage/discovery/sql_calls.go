package discovery

import (
	"fmt"
	"regexp"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
)

// sql_calls.go derives db_operation facts from raw SQL string literals passed
// to known query APIs — Go database/sql/sqlx/pgx, Python DB-API/SQLAlchemy
// text, node-postgres, Spring JdbcTemplate. This is the language-agnostic leg
// of the deterministic db floor (F6): the JVM repository deriver covers Spring
// Data, this covers everyone who writes SQL by hand.
//
// Precision gate (invariant #6), both conditions required:
//   - the callee is a curated query/exec method name, AND
//   - a leading string-literal argument parses as a SQL statement whose table
//     is extractable from its own text.
//
// The statement names its table, so the emitted fact is never a guess.

// sqlCallees are the method names (lowercased, last dotted segment) that take
// SQL text. Deliberately curated, not "anything with a SQL-looking string":
// a log message that happens to start with "select" must never become a fact.
var sqlCallees = map[string]struct{}{
	"query": {}, "queryrow": {}, "querycontext": {}, "queryrowcontext": {},
	"exec": {}, "execcontext": {}, "execute": {}, "executemany": {},
	"queryx": {}, "queryrowx": {}, "namedexec": {}, "namedquery": {},
	"get": {}, "select": {}, // sqlx convenience APIs
	"queryforobject": {}, "queryforlist": {}, "update": {}, "batchupdate": {}, // JdbcTemplate
	"fetch_all": {}, "fetch_one": {}, "fetch_val": {},
}

// DeterministicSQLOperations scans call sites for SQL literals and emits one
// high-level db_operation per (table, operation-kind), the same granularity as
// the repository deriver and the (resource, operation) dedup key.
func DeterministicSQLOperations(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil {
		return nil
	}
	type agg struct {
		table, opKind string
		loc           candidateLocation
		ev            candidateEvidence
	}
	seen := map[string]*agg{}
	var order []string

	forEachCall(idx, func(cs astpkg.CallSite) {
		_, callee := splitCall(cs)
		if _, ok := sqlCallees[strings.ToLower(callee)]; !ok {
			return
		}
		opKind, table := parseSQLStatement(leadingStringLiteral(cs.Arguments))
		if table == "" {
			return
		}
		loc := callLoc(cs)
		if loc.File == "" {
			return
		}
		key := strings.ToLower(table + "|" + opKind)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = &agg{table: table, opKind: opKind, loc: loc, ev: callEvidence(cs)}
		order = append(order, key)
	})

	out := make([]candidate, 0, len(order))
	for _, key := range order {
		a := seen[key]
		out = append(out, candidate{
			Type:       "db_operation",
			Name:       a.opKind + " " + a.table,
			Summary:    fmt.Sprintf("AST-derived %s on %s (raw SQL)", a.opKind, a.table),
			Confidence: 1.0,
			Tags:       []string{"deterministic", "sql"},
			Details: map[string]any{
				"table":         a.table,
				"operation":     a.opKind,
				"discovered_by": "ast_sql_literal",
			},
			Locations: []candidateLocation{a.loc},
			Evidence:  []candidateEvidence{a.ev},
		})
	}
	return out
}

// leadingStringLiteral returns the first string-literal argument among the
// first two positions (Go's QueryContext takes ctx first; Python's execute
// takes the SQL first).
func leadingStringLiteral(args []astpkg.ArgumentExpr) string {
	for i, a := range args {
		if i > 1 {
			break
		}
		src := strings.TrimSpace(a.Source)
		if len(src) >= 2 && (src[0] == '"' || src[0] == '\'' || src[0] == '`') {
			return strings.Trim(src, "\"'`")
		}
	}
	return ""
}

// sqlIdentifier matches a (possibly schema-qualified) table token after quote
// stripping. Anything fancier — subqueries, expressions, placeholders — fails
// the match, yields "", and is left to the LLM.
var sqlIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$.]*$`)

// parseSQLStatement classifies a SQL string and extracts its primary table.
// Returns ("", "") for anything that is not unambiguously one statement on one
// named table.
func parseSQLStatement(sql string) (opKind, table string) {
	s := strings.Join(strings.Fields(strings.ToLower(sql)), " ")
	if s == "" {
		return "", ""
	}
	tokenAfter := func(marker string) string {
		i := strings.Index(s, marker)
		if i < 0 {
			return ""
		}
		rest := strings.TrimSpace(s[i+len(marker):])
		if rest == "" {
			return ""
		}
		tok := strings.FieldsFunc(rest, func(r rune) bool { return r == ' ' || r == '(' || r == ';' || r == ',' })[0]
		tok = strings.Trim(tok, "`\"[]")
		if sqlIdentifier.MatchString(tok) {
			return tok
		}
		return ""
	}
	switch {
	case strings.HasPrefix(s, "select ") || strings.HasPrefix(s, "with "):
		return "read", tokenAfter(" from ")
	case strings.HasPrefix(s, "insert "):
		return "write", tokenAfter("into ")
	case strings.HasPrefix(s, "update "):
		return "write", tokenAfter("update ")
	case strings.HasPrefix(s, "delete "):
		return "delete", tokenAfter("from ")
	case strings.HasPrefix(s, "upsert ") || strings.HasPrefix(s, "merge "):
		return "write", tokenAfter("into ")
	}
	return "", ""
}
