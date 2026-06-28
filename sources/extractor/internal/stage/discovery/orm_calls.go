package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
)

// orm_calls.go derives db_operation facts from ORM call sites — the third leg
// of the deterministic db floor (F6): the repository deriver covers Spring
// Data conventions, sql_calls.go covers hand-written SQL, this covers ORM
// model calls where the MODEL NAME is statically resolvable from the call
// itself (GORM composite literals, Django Model.objects, Sequelize/Prisma
// model receivers, ActiveRecord constants).
//
// Precision gates (invariant #6 — emit nothing over a guess):
//   - per-ORM curated (receiver shape, callee) predicates; a generic .create()
//     never matches on its own;
//   - Sequelize/ActiveRecord additionally require CORROBORATION: their generic
//     verbs (create, update, count...) only count on a receiver that also uses
//     a distinctly-ORM verb (findByPk, bulkCreate, destroy_all, ...) somewhere
//     in the repo — a plain JS/Ruby factory class never corroborates;
//   - the model must be a capitalized identifier (or a prisma.<model> path),
//     never an expression.
//
// Table naming follows each ORM's DEFAULT convention (GORM/Sequelize/
// ActiveRecord pluralize snake_case; Django and Prisma keep the snake_cased
// model — their real names need app labels / @@map we cannot see). Identity
// normalizes singular/plural, so a custom-named table degrades to a near-miss
// resource name, not a wrong fact.

// ormCallFact is one matched ORM call, pre-corroboration.
type ormCallFact struct {
	orm, table, opKind string
	receiver           string // for corroboration grouping
	corroborated       bool   // true when the callee alone proves the ORM
	cs                 astpkg.CallSite
}

// DeterministicORMOperations emits one db_operation per (table, operation-
// kind) found through ORM calls, same granularity as the other two derivers.
func DeterministicORMOperations(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil {
		return nil
	}
	langOf := func(file string) string {
		if fa := idx.Files[file]; fa != nil {
			return fa.Language
		}
		return ""
	}

	var facts []ormCallFact
	trusted := map[string]struct{}{} // "orm|receiver" pairs proven by a distinctive verb
	forEachCall(idx, func(cs astpkg.CallSite) {
		var f *ormCallFact
		switch langOf(cs.File) {
		case "go":
			f = matchGORM(idx, cs)
			if f == nil {
				f = matchBun(idx, cs)
			}
		case "python":
			f = matchDjangoORM(cs)
		case "javascript", "typescript", "tsx", "jsx":
			if f = matchPrisma(cs); f == nil {
				f = matchSequelize(cs)
			}
		case "ruby":
			f = matchActiveRecord(cs)
		}
		if f == nil {
			return
		}
		f.cs = cs
		if f.corroborated {
			trusted[f.orm+"|"+f.receiver] = struct{}{}
		}
		facts = append(facts, *f)
	})

	type agg struct {
		fact ormCallFact
		loc  candidateLocation
	}
	seen := map[string]*agg{}
	var order []string
	for _, f := range facts {
		if !f.corroborated {
			if _, ok := trusted[f.orm+"|"+f.receiver]; !ok {
				continue // generic verb on an unproven receiver: not a fact
			}
		}
		loc := callLoc(f.cs)
		if loc.File == "" {
			continue
		}
		key := strings.ToLower(f.table + "|" + f.opKind)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = &agg{fact: f, loc: loc}
		order = append(order, key)
	}
	sort.Strings(order)

	out := make([]candidate, 0, len(order))
	for _, key := range order {
		a := seen[key]
		out = append(out, candidate{
			Type:       "db_operation",
			Name:       a.fact.opKind + " " + a.fact.table,
			Summary:    fmt.Sprintf("AST-derived %s on %s (via %s)", a.fact.opKind, a.fact.table, a.fact.orm),
			Confidence: 1.0,
			Tags:       []string{"deterministic", "orm:" + a.fact.orm},
			Details: map[string]any{
				"table":         a.fact.table,
				"operation":     a.fact.opKind,
				"orm":           a.fact.orm,
				"discovered_by": "ast_orm_call",
			},
			Locations: []candidateLocation{a.loc},
			Evidence:  []candidateEvidence{callEvidence(a.fact.cs)},
		})
	}
	return out
}

// --- Bun (Go) ----------------------------------------------------------------

var bunOps = map[string]string{
	"NewSelect": "read",
	"NewInsert": "write",
	"NewUpdate": "write",
	"NewDelete": "delete",
}

func matchBun(idx *astpkg.ProjectIndex, cs astpkg.CallSite) *ormCallFact {
	r, callee := splitCall(cs)
	opKind, ok := bunOps[callee]
	if !ok {
		return nil
	}
	rl := strings.ToLower(r)
	if rl != "db" && rl != "tx" && !strings.Contains(rl, "bun") {
		return nil
	}
	table := bunTableForCall(idx, cs)
	if table == "" {
		return nil
	}
	return &ormCallFact{
		orm: "bun", table: table,
		opKind: opKind, receiver: r, corroborated: true,
	}
}

var bunModelRE = regexp.MustCompile(`\.Model\s*\(\s*&?\s*(?:\[\]\s*)?([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)(?:\s*\{\s*\})?\s*\)`)

func bunTableForCall(idx *astpkg.ProjectIndex, cs astpkg.CallSite) string {
	src := callWindowSource(idx, cs, 8)
	if src != "" {
		if m := bunModelRE.FindStringSubmatch(src); len(m) == 2 {
			model := strings.TrimSpace(m[1])
			if dot := strings.LastIndexByte(model, '.'); dot >= 0 {
				model = model[dot+1:]
			}
			if isCapitalizedIdent(model) {
				return pluralizeSnake(snakeCase(model))
			}
			if isLowerIdent(model) {
				if typ := idx.LocalTypes[cs.Caller+"."+model]; typ != "" {
					typ = strings.TrimPrefix(strings.TrimPrefix(typ, "*"), "[]")
					if dot := strings.LastIndexByte(typ, '.'); dot >= 0 {
						typ = typ[dot+1:]
					}
					if isCapitalizedIdent(typ) {
						return pluralizeSnake(snakeCase(typ))
					}
				}
				if len(model) > 2 {
					return pluralizeSnake(snakeCase(model))
				}
			}
		}
	}
	return tableFromGoRepositoryPath(cs.File)
}

func callWindowSource(idx *astpkg.ProjectIndex, cs astpkg.CallSite, extraLines int) string {
	if idx == nil || idx.RepoRoot == "" || cs.File == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(idx.RepoRoot, cs.File))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	start := int(cs.Range.StartLine)
	if start < 0 || start >= len(lines) {
		return ""
	}
	if start > extraLines {
		start -= extraLines
	} else {
		start = 0
	}
	end := int(cs.Range.StartLine) + extraLines + 1
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start:end], "\n")
}

func tableFromGoRepositoryPath(path string) string {
	path = filepath.ToSlash(path)
	parts := strings.Split(path, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.TrimSpace(parts[i])
		if strings.HasSuffix(part, "_repo") {
			name := strings.TrimSuffix(part, "_repo")
			if name != "" {
				return pluralizeSnake(name)
			}
		}
		if strings.HasSuffix(part, "repo") && len(part) > len("repo") {
			name := strings.TrimSuffix(part, "repo")
			if name != "" {
				return pluralizeSnake(name)
			}
		}
	}
	return ""
}

// --- GORM (Go) ---------------------------------------------------------------

var gormOps = map[string]string{
	"Create": "write", "Save": "write", "Updates": "write", "Delete": "write",
	"First": "read", "Find": "read", "Take": "read", "Last": "read",
}

// matchGORM accepts db/tx/gorm receivers whose model is statically known:
// either a struct composite literal (&Order{} / []Order{}) or a local variable
// whose declared type the index already resolved (LocalTypes). Anything else
// is left to the LLM.
func matchGORM(idx *astpkg.ProjectIndex, cs astpkg.CallSite) *ormCallFact {
	r, callee := splitCall(cs)
	opKind, ok := gormOps[callee]
	if !ok {
		return nil
	}
	rl := strings.ToLower(r)
	if !strings.Contains(rl, "db") && !strings.Contains(rl, "gorm") && rl != "tx" {
		return nil
	}
	model := goCompositeLiteralModel(cs.Arguments)
	if model == "" {
		model = goLocalModelArg(idx, cs)
	}
	if model == "" {
		return nil
	}
	return &ormCallFact{
		orm: "gorm", table: pluralizeSnake(snakeCase(model)),
		opKind: opKind, receiver: r, corroborated: true, // the resolved model corroborates
	}
}

// goLocalModelArg resolves the idiomatic `db.First(&order, id)` form: the
// argument is a plain local whose declared type the parser recorded
// (LocalTypes["getOrder.order"] = "Order"). Still static — no guessing.
func goLocalModelArg(idx *astpkg.ProjectIndex, cs astpkg.CallSite) string {
	if idx == nil || len(cs.Arguments) == 0 {
		return ""
	}
	name := strings.TrimPrefix(strings.TrimSpace(cs.Arguments[0].Source), "&")
	if !isLowerIdent(name) {
		return ""
	}
	typ := idx.LocalTypes[cs.Caller+"."+name]
	typ = strings.TrimPrefix(typ, "*")
	typ = strings.TrimPrefix(typ, "[]")
	typ = strings.TrimPrefix(typ, "*")
	if dot := strings.LastIndexByte(typ, '.'); dot >= 0 {
		typ = typ[dot+1:]
	}
	if !isCapitalizedIdent(typ) {
		return ""
	}
	return typ
}

// goCompositeLiteralModel extracts the struct name from the first argument of
// the form &Order{...}, Order{...}, []Order{} or &[]Order{}.
func goCompositeLiteralModel(args []astpkg.ArgumentExpr) string {
	if len(args) == 0 {
		return ""
	}
	src := strings.TrimSpace(args[0].Source)
	src = strings.TrimPrefix(src, "&")
	src = strings.TrimPrefix(src, "[]")
	brace := strings.IndexByte(src, '{')
	if brace <= 0 {
		return ""
	}
	name := src[:brace]
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		name = name[dot+1:] // pkg.Order{} → Order
	}
	if !isCapitalizedIdent(name) {
		return ""
	}
	return name
}

// --- Django ORM (Python) ------------------------------------------------------

var djangoORMOps = map[string]string{
	"filter": "read", "get": "read", "all": "read", "exists": "read",
	"count": "read", "values": "read", "values_list": "read", "first": "read", "last": "read",
	"create": "write", "bulk_create": "write", "update": "write",
	"update_or_create": "write", "get_or_create": "write", "delete": "write",
}

// matchDjangoORM accepts the unmistakable <Model>.objects.<op> shape.
func matchDjangoORM(cs astpkg.CallSite) *ormCallFact {
	r, callee := splitCall(cs)
	opKind, ok := djangoORMOps[callee]
	if !ok || !strings.HasSuffix(r, ".objects") {
		return nil
	}
	model := strings.TrimSuffix(r, ".objects")
	if dot := strings.LastIndexByte(model, '.'); dot >= 0 {
		model = model[dot+1:] // models.Order.objects → Order
	}
	if !isCapitalizedIdent(model) {
		return nil
	}
	return &ormCallFact{
		orm: "django-orm", table: snakeCase(model),
		opKind: opKind, receiver: r, corroborated: true, // .objects. is the proof
	}
}

// --- Prisma (Node) -------------------------------------------------------------

var prismaOps = map[string]string{
	"findMany": "read", "findUnique": "read", "findFirst": "read",
	"count": "read", "aggregate": "read", "groupBy": "read",
	"create": "write", "createMany": "write", "update": "write", "updateMany": "write",
	"upsert": "write", "delete": "write", "deleteMany": "write",
}

// matchPrisma accepts prisma.<model>.<op> (also this.prisma / client suffixes).
func matchPrisma(cs astpkg.CallSite) *ormCallFact {
	r, callee := splitCall(cs)
	opKind, ok := prismaOps[callee]
	if !ok {
		return nil
	}
	segs := strings.Split(r, ".")
	if len(segs) < 2 {
		return nil
	}
	model := segs[len(segs)-1]
	owner := strings.ToLower(segs[len(segs)-2])
	if !strings.Contains(owner, "prisma") || !isLowerIdent(model) {
		return nil
	}
	return &ormCallFact{
		orm: "prisma", table: snakeCase(model),
		opKind: opKind, receiver: r, corroborated: true, // the prisma client path is the proof
	}
}

// --- Sequelize (Node) ----------------------------------------------------------

var sequelizeOps = map[string]string{
	"findAll": "read", "findOne": "read", "findByPk": "read", "findAndCountAll": "read", "count": "read",
	"create": "write", "bulkCreate": "write", "update": "write", "upsert": "write", "destroy": "write",
}

// Distinctly-Sequelize verbs; a receiver using one is trusted for the generic
// verbs too. A plain factory class (User.create) never corroborates.
var sequelizeDistinctive = map[string]struct{}{
	"findAll": {}, "findByPk": {}, "findAndCountAll": {}, "bulkCreate": {}, "upsert": {}, "destroy": {},
}

func matchSequelize(cs astpkg.CallSite) *ormCallFact {
	r, callee := splitCall(cs)
	opKind, ok := sequelizeOps[callee]
	if !ok || !isCapitalizedIdent(r) {
		return nil
	}
	_, distinctive := sequelizeDistinctive[callee]
	return &ormCallFact{
		orm: "sequelize", table: pluralizeSnake(snakeCase(r)),
		opKind: opKind, receiver: r, corroborated: distinctive,
	}
}

// --- ActiveRecord (Ruby) -------------------------------------------------------

var activeRecordOps = map[string]string{
	"find": "read", "find_by": "read", "find_by!": "read", "where": "read",
	"first": "read", "all": "read", "exists?": "read", "pluck": "read", "count": "read",
	"create": "write", "create!": "write", "update": "write", "update!": "write",
	"update_all": "write", "destroy": "write", "destroy!": "write",
	"destroy_all": "write", "delete_all": "write",
}

var activeRecordDistinctive = map[string]struct{}{
	"find_by": {}, "find_by!": {}, "create!": {}, "update_all": {},
	"destroy_all": {}, "delete_all": {}, "exists?": {}, "pluck": {},
}

func matchActiveRecord(cs astpkg.CallSite) *ormCallFact {
	r, callee := splitCall(cs)
	opKind, ok := activeRecordOps[callee]
	if !ok || !isCapitalizedIdent(r) {
		return nil
	}
	_, distinctive := activeRecordDistinctive[callee]
	return &ormCallFact{
		orm: "activerecord", table: pluralizeSnake(snakeCase(r)),
		opKind: opKind, receiver: r, corroborated: distinctive,
	}
}

// --- naming helpers ------------------------------------------------------------

func isCapitalizedIdent(s string) bool {
	if s == "" || s[0] < 'A' || s[0] > 'Z' {
		return false
	}
	return isIdentTail(s[1:])
}

func isLowerIdent(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	return isIdentTail(s[1:])
}

func isIdentTail(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}

// snakeCase converts CamelCase to snake_case: OrderItem → order_item.
func snakeCase(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}

// pluralizeSnake applies the default English pluralization the ORMs use:
// order → orders, box → boxes, category → categories. Irregulars degrade to a
// near-miss resource name that singular/plural identity normalization absorbs.
func pluralizeSnake(s string) string {
	switch {
	case s == "":
		return s
	case strings.HasSuffix(s, "s") || strings.HasSuffix(s, "x") || strings.HasSuffix(s, "z") ||
		strings.HasSuffix(s, "ch") || strings.HasSuffix(s, "sh"):
		return s + "es"
	case len(s) > 1 && strings.HasSuffix(s, "y") && !strings.ContainsAny(s[len(s)-2:len(s)-1], "aeiou"):
		return s[:len(s)-1] + "ies"
	default:
		return s + "s"
	}
}
