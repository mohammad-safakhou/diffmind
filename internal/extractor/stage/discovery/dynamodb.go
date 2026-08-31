package discovery

import (
	"fmt"
	"sort"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/extractor/ast"
)

// DeterministicDynamoDBOperations detects direct Spring Cloud AWS
// DynamoDbTemplate reads/writes. This covers services that do not expose a
// repository interface for DynamoDB and instead call the template directly.
func DeterministicDynamoDBOperations(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil || len(idx.CallGraph) == 0 {
		return nil
	}
	table, tableKey := canonicalDynamoDBTable(idx)
	if table == "" {
		return nil
	}
	type agg struct {
		opKind string
		loc    candidateLocation
		hits   int
	}
	seen := map[string]*agg{}
	var order []string
	for _, sites := range idx.CallGraph {
		for _, cs := range sites {
			if !isDynamoDBTemplateCall(idx, cs) {
				continue
			}
			opKind := dynamoDBTemplateOperation(cs.CalleeRaw)
			if opKind == "" {
				continue
			}
			key := table + "|" + opKind
			a := seen[key]
			if a == nil {
				a = &agg{
					opKind: opKind,
					loc: candidateLocation{
						File:      cs.File,
						StartLine: int(cs.Range.StartLine) + 1,
						EndLine:   int(cs.Range.EndLine) + 1,
					},
				}
				seen[key] = a
				order = append(order, key)
			}
			a.hits++
		}
	}
	sort.Strings(order)
	out := make([]candidate, 0, len(order))
	for _, key := range order {
		a := seen[key]
		operation := a.opKind + " " + table
		out = append(out, candidate{
			Type:       "db_operation",
			Name:       operation,
			Summary:    fmt.Sprintf("AST-derived DynamoDB %s on %s via DynamoDbTemplate", a.opKind, table),
			Confidence: 1.0,
			Tags:       []string{"deterministic", "dynamodb", "spring-cloud-aws"},
			Details: map[string]any{
				"table":           table,
				"table_or_entity": table,
				"operation":       a.opKind,
				"database_type":   "dynamodb",
				"platform":        "dynamodb",
				"repository":      "DynamoDbTemplate",
				"table_property":  tableKey,
				"discovered_by":   "ast_dynamodb_template_call",
			},
			Locations: []candidateLocation{a.loc},
			Evidence: []candidateEvidence{{
				File:      a.loc.File,
				StartLine: a.loc.StartLine,
				EndLine:   a.loc.EndLine,
				Snippet:   "DynamoDbTemplate " + a.opKind + " call resolved to " + table,
				Source:    "deterministic_ast_dynamodb",
			}},
		})
	}
	return out
}

func isDynamoDBTemplateCall(idx *astpkg.ProjectIndex, cs astpkg.CallSite) bool {
	method := strings.ToLower(strings.TrimSpace(cs.CalleeRaw))
	if dynamoDBTemplateOperation(method) == "" {
		return false
	}
	recv := strings.TrimSpace(cs.ReceiverRaw)
	if recv == "" {
		return false
	}
	if strings.Contains(strings.ToLower(recv), "dynamodb") {
		return true
	}
	caller := strings.TrimSpace(cs.Caller)
	if caller == "" {
		return false
	}
	if typ := idx.LocalTypes[caller+"."+recv]; strings.Contains(strings.ToLower(typ), "dynamodbtemplate") {
		return true
	}
	className := caller
	if dot := strings.LastIndex(className, "."); dot > 0 {
		className = className[:dot]
	}
	return strings.Contains(strings.ToLower(idx.FieldTypes[className+"."+recv]), "dynamodbtemplate")
}

func dynamoDBTemplateOperation(method string) string {
	switch lastTypeSegment(strings.ToLower(strings.TrimSpace(method))) {
	case "save", "putitem", "updateitem":
		return "write"
	case "load", "getitem", "query", "scan":
		return "read"
	case "delete", "deleteitem":
		return "delete"
	}
	return ""
}

func canonicalDynamoDBTable(idx *astpkg.ProjectIndex) (table, key string) {
	type candidate struct {
		key, value string
	}
	var base []candidate
	for _, path := range sortedConfigPaths(idx) {
		if configProfile(path) != "" {
			continue
		}
		cf := idx.Configs[path]
		if cf == nil {
			continue
		}
		for _, e := range cf.Entries {
			if e.Profile != "" {
				continue
			}
			k := strings.ToLower(strings.TrimSpace(e.Key))
			if !strings.Contains(k, "dynamodb") || !strings.Contains(k, "table") {
				continue
			}
			v := normalizeDynamoDBTableName(e.Value)
			if v == "" {
				continue
			}
			base = append(base, candidate{key: e.Key, value: v})
		}
	}
	if len(base) == 1 {
		return base[0].value, base[0].key
	}
	return "", ""
}

func normalizeDynamoDBTableName(raw string) string {
	s := strings.Trim(strings.TrimSpace(stripPlaceholderDefault(raw)), `"'`)
	if s == "" || IsPlaceholder(s) {
		return ""
	}
	s = stripTemplateReferences(s)
	s = strings.Trim(s, "-_/ ")
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return ""
	}
	if strings.Contains(s, "://") || strings.HasPrefix(strings.ToLower(s), "arn:") {
		return ""
	}
	return strings.ToLower(strings.NewReplacer("_", "-").Replace(s))
}
