package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
	"gopkg.in/yaml.v3"
)

const maxOpenAPIBytes = 4 << 20
const maxOpenAPIFields = 1000

// EnrichHTTPContractsFromOpenAPI reads a bounded OpenAPI 3 subset from local
// repository files. Remote references are never fetched. Unsupported or
// malformed documents produce warnings and no fabricated fields.
func EnrichHTTPContractsFromOpenAPI(repoPath string, exposures []model.Exposure) []string {
	files, warnings := openAPIFiles(repoPath)
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("OpenAPI read %s: %v", filepath.Base(path), err))
			continue
		}
		if len(body) > maxOpenAPIBytes {
			warnings = append(warnings, fmt.Sprintf("OpenAPI file %s exceeds 4 MiB", filepath.Base(path)))
			continue
		}
		var doc map[string]any
		if err := yaml.Unmarshal(body, &doc); err != nil {
			warnings = append(warnings, fmt.Sprintf("OpenAPI parse %s: %v", filepath.Base(path), err))
			continue
		}
		version := stringFromMap(doc, "openapi")
		if !strings.HasPrefix(version, "3.0.") {
			continue
		}
		source, err := filepath.Rel(repoPath, path)
		if err != nil {
			source = filepath.Base(path)
		}
		source = filepath.ToSlash(source)
		paths, _ := doc["paths"].(map[string]any)
		for route, rawPath := range paths {
			pathItem, _ := rawPath.(map[string]any)
			for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options"} {
				op, _ := pathItem[method].(map[string]any)
				if op == nil {
					continue
				}
				fields := openAPIParameters(pathItem["parameters"], source, body)
				fields = append(fields, openAPIParameters(op["parameters"], source, body)...)
				fields = append(fields, openAPIRequestFields(doc, op, source, body)...)
				if len(fields) > maxOpenAPIFields {
					warnings = append(warnings, fmt.Sprintf("OpenAPI operation %s %s exceeds field limit", strings.ToUpper(method), route))
					continue
				}
				for i := range exposures {
					base := &exposures[i].BaseEntity
					exposureMethod, exposurePath := exposureMethodPath(*base)
					if !strings.EqualFold(exposureMethod, method) || exposurePath != route {
						continue
					}
					if base.Details == nil {
						base.Details = map[string]any{}
					}
					base.Details["request_fields"] = mergeOpenAPIFields(base.Details["request_fields"], fields)
					base.Details["contract_source"] = "openapi_3_0"
				}
			}
		}
	}
	sort.Strings(warnings)
	return warnings
}

func openAPIFiles(root string) ([]string, []string) {
	var files, warnings []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && skipOpenAPIDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) >= 20 {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		name := strings.ToLower(d.Name())
		if name == "openapi.yaml" || name == "openapi.yml" || name == "openapi.json" || name == "swagger.yaml" || name == "swagger.yml" || name == "swagger.json" {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, warnings
}

func skipOpenAPIDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "target", "build", "dist", ".venv", "venv", ".tox", ".nox":
		return true
	}
	return false
}

func openAPIParameters(raw any, source string, body []byte) []map[string]any {
	items, _ := raw.([]any)
	var out []map[string]any
	for _, item := range items {
		p, _ := item.(map[string]any)
		if p == nil || p["$ref"] != nil {
			continue
		}
		name, location := stringFromMap(p, "name"), stringFromMap(p, "in")
		if name == "" || location == "" {
			continue
		}
		schema, _ := p["schema"].(map[string]any)
		out = append(out, map[string]any{"name": name, "location": location, "type": stringFromMap(schema, "type"), "required": boolFromMap(p, "required"), "nullable": boolFromMap(schema, "nullable"), "source": "openapi_3_0", "source_file": source, "source_line": sourceLine(body, name)})
	}
	return out
}

func openAPIRequestFields(doc, op map[string]any, source string, body []byte) []map[string]any {
	request, _ := op["requestBody"].(map[string]any)
	if request == nil {
		return nil
	}
	content, _ := request["content"].(map[string]any)
	if content == nil {
		return nil
	}
	media, _ := content["application/json"].(map[string]any)
	if media == nil {
		for _, value := range content {
			media, _ = value.(map[string]any)
			if media != nil {
				break
			}
		}
	}
	schema, _ := media["schema"].(map[string]any)
	if schema == nil {
		return nil
	}
	return openAPISchemaFields(doc, schema, "body", "", source, body, 0, map[string]bool{})
}

func openAPISchemaFields(doc, schema map[string]any, location, prefix, source string, body []byte, depth int, visiting map[string]bool) []map[string]any {
	if depth > 8 || schema == nil {
		return nil
	}
	pointer := stringFromMap(schema, "$ref")
	if pointer != "" {
		if !strings.HasPrefix(pointer, "#/components/schemas/") || visiting[pointer] {
			return nil
		}
		visiting[pointer] = true
		defer delete(visiting, pointer)
		name := strings.TrimPrefix(pointer, "#/components/schemas/")
		components, _ := doc["components"].(map[string]any)
		schemas, _ := components["schemas"].(map[string]any)
		schema, _ = schemas[name].(map[string]any)
		if schema == nil {
			return nil
		}
	}
	required := map[string]bool{}
	if list, ok := schema["required"].([]any); ok {
		for _, value := range list {
			required[fmt.Sprint(value)] = true
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	keys := make([]string, 0, len(properties))
	for key := range properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out []map[string]any
	for _, name := range keys {
		child, _ := properties[name].(map[string]any)
		fieldName := name
		if prefix != "" {
			fieldName = prefix + "." + name
		}
		field := map[string]any{"name": fieldName, "location": location, "type": stringFromMap(child, "type"), "required": required[name], "nullable": boolFromMap(child, "nullable"), "schema_pointer": pointer, "source": "openapi_3_0", "source_file": source, "source_line": sourceLine(body, name)}
		out = append(out, field)
		if stringFromMap(child, "type") == "object" || child["$ref"] != nil {
			out = append(out, openAPISchemaFields(doc, child, location, fieldName, source, body, depth+1, visiting)...)
		}
		if len(out) >= maxOpenAPIFields {
			return out[:maxOpenAPIFields]
		}
	}
	return out
}

func mergeOpenAPIFields(existing any, fields []map[string]any) []any {
	var out []any
	switch value := existing.(type) {
	case []any:
		out = append(out, value...)
	case []map[string]any:
		for _, item := range value {
			out = append(out, item)
		}
	case nil:
	default:
		out = append(out, value)
	}
	seen := map[string]bool{}
	for _, item := range out {
		if m, ok := item.(map[string]any); ok {
			seen[stringFromMap(m, "location")+"\x00"+stringFromMap(m, "name")] = true
		}
	}
	for _, field := range fields {
		key := stringFromMap(field, "location") + "\x00" + stringFromMap(field, "name")
		if !seen[key] {
			out = append(out, field)
			seen[key] = true
		}
	}
	return out
}
func exposureMethodPath(base model.BaseEntity) (string, string) {
	method, path := stringFromMap(base.Details, "method"), stringFromMap(base.Details, "path")
	if method == "" || path == "" {
		parts := strings.SplitN(base.Name, " ", 2)
		if len(parts) == 2 {
			method, path = parts[0], parts[1]
		}
	}
	return strings.ToLower(method), path
}
func stringFromMap(m map[string]any, key string) string {
	if m == nil || m[key] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(m[key]))
}
func boolFromMap(m map[string]any, key string) bool { value, _ := m[key].(bool); return value }
func sourceLine(body []byte, needle string) int {
	for i, line := range strings.Split(string(body), "\n") {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	return 0
}
