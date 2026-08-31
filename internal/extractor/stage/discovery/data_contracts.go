package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
)

// EnrichDataContracts fills field-level IO hints consumed by the Protocol writer's
// Flow.DataDependencies synthesis. It is additive: existing explicit details win.
func EnrichDataContracts(idx *astpkg.ProjectIndex, exposures []model.Exposure, deps []model.Dependency) {
	if idx == nil {
		return
	}
	enrichExposureDataFields(idx, exposures)
	enrichDependencyDataFields(idx, deps)
}

func enrichExposureDataFields(idx *astpkg.ProjectIndex, exposures []model.Exposure) {
	for i := range exposures {
		e := &exposures[i].BaseEntity
		if !routeInputTypes[e.Type] || hasAnyDetail(e.Details, "body_fields", "request_fields") {
			continue
		}
		if sym, ok := handlerSymbolForExposure(idx, e); ok {
			if fields := bodyFieldsFromHandler(idx, sym); len(fields) > 0 {
				ensureDetails(e)
				e.Details["body_fields"] = fields
				continue
			}
		}
		for _, in := range e.Inputs {
			if strings.EqualFold(strings.TrimSpace(in.Description), "body") {
				if fields := fieldsForType(idx, firstEntityFile(*e), in.Type); len(fields) > 0 {
					ensureDetails(e)
					e.Details["body_fields"] = fields
					break
				}
			}
		}
	}
}

func bodyFieldsFromHandler(idx *astpkg.ProjectIndex, sym astpkg.SymbolDef) []string {
	for _, p := range sym.Parameters {
		in, ok := paramInput(p)
		if !ok || in.Description != "body" {
			continue
		}
		if fields := fieldsForType(idx, sym.File, p.Type); len(fields) > 0 {
			return fields
		}
	}
	return nil
}

func enrichDependencyDataFields(idx *astpkg.ProjectIndex, deps []model.Dependency) {
	for i := range deps {
		d := &deps[i].BaseEntity
		switch d.Type {
		case "db_operation":
			enrichDBOperationFields(idx, d)
		case "queue_publish":
			enrichQueuePublishFields(idx, d)
		}
	}
}

func enrichDBOperationFields(idx *astpkg.ProjectIndex, d *model.BaseEntity) {
	if d == nil || hasAnyDetail(d.Details, "writes", "write_columns", "columns_written", "reads", "read_columns", "columns_read", "columns") {
		return
	}
	ensureDetails(d)
	op := strings.ToLower(strings.TrimSpace(scalarDataDetail(d.Details, "operation", "operation_kind")))
	if sql := scalarDataDetail(d.Details, "sql", "statement", "query"); sql != "" {
		for k, v := range sqlColumnDetails(sql) {
			if len(v) > 0 {
				d.Details[k] = v
			}
		}
		return
	}
	entity := scalarDataDetail(d.Details, "entity", "model", "class")
	if entity == "" {
		entity = strings.TrimSuffix(lastIdentOf(scalarDataDetail(d.Details, "repository", "repository_class", "client")), "Repository")
	}
	fields := fieldsForType(idx, firstEntityFile(*d), entity)
	if len(fields) == 0 {
		return
	}
	switch op {
	case "write", "create", "update", "upsert":
		d.Details["writes"] = fields
	case "read", "select":
		d.Details["reads"] = fields
	default:
		d.Details["columns"] = fields
	}
}

func enrichQueuePublishFields(idx *astpkg.ProjectIndex, d *model.BaseEntity) {
	if d == nil || hasAnyDetail(d.Details, "message_fields", "payload_fields") {
		return
	}
	cs, ok := callSiteForEntity(idx, *d)
	if !ok {
		return
	}
	fields := messageFieldsFromPublishCall(cs)
	if len(fields) == 0 {
		return
	}
	ensureDetails(d)
	d.Details["message_fields"] = fields
}

func callSiteForEntity(idx *astpkg.ProjectIndex, e model.BaseEntity) (astpkg.CallSite, bool) {
	if idx == nil || len(e.Locations) == 0 {
		return astpkg.CallSite{}, false
	}
	loc := e.Locations[0]
	fa := idx.Files[loc.File]
	if fa == nil {
		return astpkg.CallSite{}, false
	}
	for _, cs := range fa.Calls {
		line := int(cs.Range.StartLine) + 1
		if line >= loc.StartLine && line <= loc.EndLine {
			return cs, true
		}
	}
	return astpkg.CallSite{}, false
}

func messageFieldsFromPublishCall(cs astpkg.CallSite) []string {
	var fields []string
	for _, arg := range cs.Arguments {
		src := strings.TrimSpace(arg.Source)
		if src == "" || arg.Index == 0 && looksLikeDestinationArgument(src) {
			continue
		}
		fields = append(fields, fieldsFromPayloadExpression(src)...)
	}
	return cleanDataFieldNames(fields)
}

func fieldsFromPayloadExpression(src string) []string {
	var out []string
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`\.([A-Za-z_][A-Za-z0-9_]*)\s*\(`),
		regexp.MustCompile(`\bset([A-Z][A-Za-z0-9_]*)\s*\(`),
		regexp.MustCompile(`["']([A-Za-z_][A-Za-z0-9_]*)["']\s*:`),
		regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*:`),
		regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*=`),
	} {
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			if len(m) < 2 {
				continue
			}
			name := lowerFirst(m[1])
			if !payloadFieldDenylist[strings.ToLower(name)] {
				out = append(out, name)
			}
		}
	}
	return cleanDataFieldNames(out)
}

var payloadFieldDenylist = map[string]bool{
	"builder": true, "build": true, "body": true, "messagebody": true,
	"queueurl": true, "topicarn": true, "topic": true, "queue": true,
}

func looksLikeDestinationArgument(src string) bool {
	s := strings.ToLower(strings.Trim(src, ` "'`))
	return strings.Contains(s, "queue") || strings.Contains(s, "topic") ||
		strings.Contains(s, "arn") || strings.Contains(s, "url") ||
		strings.HasPrefix(s, "${")
}

func fieldsForType(idx *astpkg.ProjectIndex, preferredFile, typ string) []string {
	typ = cleanTypeName(typ)
	if idx == nil || typ == "" {
		return nil
	}
	if preferredFile != "" {
		if fields := fieldsForTypeInFile(idx, preferredFile, typ); len(fields) > 0 {
			return fields
		}
	}
	paths := make([]string, 0, len(idx.Files))
	for path := range idx.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if path == preferredFile {
			continue
		}
		if fields := fieldsForTypeInFile(idx, path, typ); len(fields) > 0 {
			return fields
		}
	}
	return nil
}

func fieldsForTypeInFile(idx *astpkg.ProjectIndex, relPath, typ string) []string {
	fa := idx.Files[relPath]
	if fa == nil {
		return nil
	}
	src := sourceForIndexedFile(idx, relPath)
	if src == "" {
		return nil
	}
	switch fa.Language {
	case "java", "kotlin":
		return javaTypeFields(src, typ)
	case "go":
		return goTypeFields(src, typ)
	default:
		return nil
	}
}

func sourceForIndexedFile(idx *astpkg.ProjectIndex, relPath string) string {
	if idx == nil || idx.RepoRoot == "" || relPath == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(idx.RepoRoot, relPath))
	if err != nil {
		return ""
	}
	return string(b)
}

func javaTypeFields(src, typ string) []string {
	if fields := javaRecordFields(src, typ); len(fields) > 0 {
		return fields
	}
	re := regexp.MustCompile(`(?s)\b(?:class|interface|enum)\s+` + regexp.QuoteMeta(typ) + `\b[^{]*\{`)
	m := re.FindStringIndex(src)
	if m == nil {
		return nil
	}
	body := balancedBody(src, m[1]-1)
	var fields []string
	var pendingColumn string
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if col := javaColumnName(line); col != "" {
			pendingColumn = col
			continue
		}
		name := javaFieldName(line)
		if name == "" {
			continue
		}
		if pendingColumn != "" {
			fields = append(fields, pendingColumn)
			pendingColumn = ""
		} else {
			fields = append(fields, name)
		}
	}
	return cleanDataFieldNames(fields)
}

func javaRecordFields(src, typ string) []string {
	re := regexp.MustCompile(`(?s)\brecord\s+` + regexp.QuoteMeta(typ) + `\s*\((.*?)\)`)
	m := re.FindStringSubmatch(src)
	if len(m) < 2 {
		return nil
	}
	var out []string
	for _, part := range splitDataArgs(m[1]) {
		toks := strings.Fields(strings.TrimSpace(part))
		if len(toks) > 0 {
			out = append(out, toks[len(toks)-1])
		}
	}
	return cleanDataFieldNames(out)
}

func javaColumnName(line string) string {
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`@Column\s*\([^)]*name\s*=\s*"([^"]+)"`),
		regexp.MustCompile(`@JsonProperty\s*\(\s*"([^"]+)"`),
	} {
		if m := re.FindStringSubmatch(line); len(m) > 1 {
			return m[1]
		}
	}
	return ""
}

func javaFieldName(line string) string {
	if strings.Contains(line, "(") || strings.HasPrefix(line, "@") {
		return ""
	}
	re := regexp.MustCompile(`\b(?:private|protected|public)\s+(?:static\s+)?(?:final\s+)?[A-Za-z0-9_<>, ?.\[\]]+\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:=|;)`)
	if m := re.FindStringSubmatch(line); len(m) > 1 {
		return m[1]
	}
	return ""
}

func goTypeFields(src, typ string) []string {
	re := regexp.MustCompile(`(?s)\btype\s+` + regexp.QuoteMeta(typ) + `\s+struct\s*\{(.*?)\}`)
	m := re.FindStringSubmatch(src)
	if len(m) < 2 {
		return nil
	}
	var fields []string
	for _, raw := range strings.Split(m[1], "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if jsonName := goJSONFieldName(line); jsonName != "" {
			fields = append(fields, jsonName)
			continue
		}
		toks := strings.Fields(line)
		if len(toks) > 0 && isExportedIdentifier(toks[0]) {
			fields = append(fields, lowerFirst(toks[0]))
		}
	}
	return cleanDataFieldNames(fields)
}

func goJSONFieldName(line string) string {
	re := regexp.MustCompile("`[^`]*json:\"([^\",]+)")
	if m := re.FindStringSubmatch(line); len(m) > 1 && m[1] != "-" {
		return m[1]
	}
	return ""
}

func balancedBody(src string, openBrace int) string {
	if openBrace < 0 || openBrace >= len(src) || src[openBrace] != '{' {
		return ""
	}
	depth := 0
	for i := openBrace; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[openBrace+1 : i]
			}
		}
	}
	return ""
}

func sqlColumnDetails(sql string) map[string][]string {
	op, _ := parseSQLStatement(sql)
	cols := sqlColumns(sql, op)
	if len(cols) == 0 {
		return nil
	}
	switch op {
	case "write":
		return map[string][]string{"writes": cols}
	case "read":
		return map[string][]string{"reads": cols}
	default:
		return map[string][]string{"columns": cols}
	}
}

func sqlColumns(sql, op string) []string {
	s := strings.Join(strings.Fields(sql), " ")
	lower := strings.ToLower(s)
	switch op {
	case "write":
		if strings.HasPrefix(lower, "insert ") {
			if i := strings.IndexByte(s, '('); i >= 0 {
				if j := strings.IndexByte(s[i+1:], ')'); j >= 0 {
					return cleanDataFieldNames(splitDataArgs(s[i+1 : i+1+j]))
				}
			}
		}
		if strings.HasPrefix(lower, "update ") {
			if i := strings.Index(lower, " set "); i >= 0 {
				rest := s[i+5:]
				if j := strings.Index(strings.ToLower(rest), " where "); j >= 0 {
					rest = rest[:j]
				}
				var out []string
				for _, assign := range splitDataArgs(rest) {
					if k := strings.Index(assign, "="); k > 0 {
						out = append(out, strings.TrimSpace(assign[:k]))
					}
				}
				return cleanDataFieldNames(out)
			}
		}
	case "read":
		if strings.HasPrefix(lower, "select ") {
			from := strings.Index(lower, " from ")
			if from > len("select ") {
				list := strings.TrimSpace(s[len("select "):from])
				if list != "*" && !strings.Contains(list, "(") {
					return cleanDataFieldNames(splitDataArgs(list))
				}
			}
		}
	}
	return nil
}

func splitDataArgs(s string) []string {
	var out []string
	depth := 0
	start := 0
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if quote != 0 {
			if ch == '\\' && quote != '`' {
				i++
			} else if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '"', '\'', '`':
			quote = ch
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}', '>':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

func cleanDataFieldNames(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(strings.Trim(s, "`\"' "))
		if i := strings.LastIndexByte(s, '.'); i >= 0 {
			s = s[i+1:]
		}
		if s == "" || s == "*" || seen[s] || !isFieldIdentifierLike(s) {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func cleanTypeName(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(s, "*"))
	s = strings.TrimPrefix(s, "[]")
	if i := strings.LastIndexByte(s, '.'); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.IndexAny(s, "<["); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func scalarDataDetail(d map[string]any, keys ...string) string {
	for _, key := range keys {
		if d == nil {
			return ""
		}
		if v, ok := d[key]; ok {
			s := strings.TrimSpace(strings.Trim(fmt.Sprint(v), `"'`))
			if s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func hasAnyDetail(d map[string]any, keys ...string) bool {
	for _, key := range keys {
		if d == nil {
			continue
		}
		if v, ok := d[key]; ok {
			switch t := v.(type) {
			case []string:
				if len(t) > 0 {
					return true
				}
			case []any:
				if len(t) > 0 {
					return true
				}
			case string:
				if strings.TrimSpace(t) != "" {
					return true
				}
			default:
				if v != nil {
					return true
				}
			}
		}
	}
	return false
}

func ensureDetails(e *model.BaseEntity) {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
}

func firstEntityFile(e model.BaseEntity) string {
	if len(e.Locations) == 0 {
		return ""
	}
	return e.Locations[0].File
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] >= 'A' && b[0] <= 'Z' {
		b[0] += 'a' - 'A'
	}
	return string(b)
}

func isExportedIdentifier(s string) bool {
	return len(s) > 0 && s[0] >= 'A' && s[0] <= 'Z'
}

func isFieldIdentifierLike(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}
