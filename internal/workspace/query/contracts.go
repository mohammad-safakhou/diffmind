package query

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/archgraph"
)

type ContractField struct {
	EndpointID    string `json:"endpoint_id"`
	Service       string `json:"service"`
	Method        string `json:"method,omitempty"`
	Path          string `json:"path,omitempty"`
	Location      string `json:"location"`
	Name          string `json:"name"`
	Type          string `json:"type,omitempty"`
	Required      bool   `json:"required"`
	Nullable      bool   `json:"nullable,omitempty"`
	SchemaPointer string `json:"schema_pointer,omitempty"`
	Source        string `json:"source"`
	SourceFile    string `json:"source_file,omitempty"`
	SourceLine    int    `json:"source_line,omitempty"`
	Evidence      any    `json:"evidence,omitempty"`
}

type ContractResponse struct {
	ProjectID string          `json:"project_id"`
	RunID     string          `json:"run_id"`
	Service   string          `json:"service,omitempty"`
	Fields    []ContractField `json:"fields"`
}

type ContractChange struct {
	Key           string         `json:"key"`
	Change        string         `json:"change"`
	Compatibility string         `json:"compatibility"`
	Before        *ContractField `json:"before,omitempty"`
	After         *ContractField `json:"after,omitempty"`
}

type ContractDiffResponse struct {
	ProjectID string           `json:"project_id"`
	FromRun   string           `json:"from_run"`
	ToRun     string           `json:"to_run"`
	Service   string           `json:"service,omitempty"`
	Changes   []ContractChange `json:"changes"`
}

func (s *Service) Contracts(projectID, runID, service string) (*ContractResponse, error) {
	run, graph, err := s.Load(projectID, runID)
	if err != nil {
		return nil, err
	}
	service = strings.TrimSpace(service)
	var fields []ContractField
	found := service == ""
	for _, svc := range graph.Services {
		if svc == nil || (service != "" && svc.Name != service) {
			continue
		}
		found = true
		for _, endpoint := range svc.HTTPRoutes {
			fields = append(fields, endpointContractFields(svc.Name, endpoint)...)
		}
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrServiceNotFound, service)
	}
	sort.Slice(fields, func(i, j int) bool { return contractFieldKey(fields[i]) < contractFieldKey(fields[j]) })
	return &ContractResponse{ProjectID: run.ProjectID, RunID: run.ID, Service: service, Fields: fields}, nil
}

func (s *Service) CompareContracts(projectID, fromRun, toRun, service string) (*ContractDiffResponse, error) {
	if strings.TrimSpace(fromRun) == "" || strings.TrimSpace(toRun) == "" {
		return nil, errors.New("from and to run IDs are required")
	}
	before, err := s.Contracts(projectID, fromRun, service)
	if err != nil {
		return nil, err
	}
	after, err := s.Contracts(projectID, toRun, service)
	if err != nil {
		return nil, err
	}
	left, right := map[string]ContractField{}, map[string]ContractField{}
	for _, field := range before.Fields {
		left[contractFieldKey(field)] = field
	}
	for _, field := range after.Fields {
		right[contractFieldKey(field)] = field
	}
	keys := map[string]bool{}
	for key := range left {
		keys[key] = true
	}
	for key := range right {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	var changes []ContractChange
	for _, key := range ordered {
		old, hadOld := left[key]
		next, hasNext := right[key]
		switch {
		case !hadOld:
			compatibility := "compatible"
			if next.Required {
				compatibility = "potentially_breaking"
			}
			copy := next
			changes = append(changes, ContractChange{Key: key, Change: "added", Compatibility: compatibility, After: &copy})
		case !hasNext:
			copy := old
			changes = append(changes, ContractChange{Key: key, Change: "removed", Compatibility: "potentially_breaking", Before: &copy})
		case old.Type != next.Type || old.Required != next.Required || old.Nullable != next.Nullable:
			compatibility := "compatible"
			if (!old.Required && next.Required) || (old.Nullable && !next.Nullable) || (old.Type != "" && next.Type != "" && old.Type != next.Type) {
				compatibility = "potentially_breaking"
			}
			oldCopy, nextCopy := old, next
			changes = append(changes, ContractChange{Key: key, Change: "modified", Compatibility: compatibility, Before: &oldCopy, After: &nextCopy})
		}
	}
	return &ContractDiffResponse{ProjectID: before.ProjectID, FromRun: before.RunID, ToRun: after.RunID, Service: service, Changes: changes}, nil
}

func endpointContractFields(service string, endpoint archgraph.EntitySummary) []ContractField {
	d := contractDetails(endpoint.Details)
	method, path := stringValue(d["method"]), stringValue(d["path"])
	if method == "" || path == "" {
		parts := strings.SplitN(endpoint.Name, " ", 2)
		if len(parts) == 2 {
			method, path = parts[0], parts[1]
		}
	}
	id := endpoint.ID
	if id == "" {
		id = strings.ToUpper(method) + " " + path
	}
	var out []ContractField
	add := func(location, source string, raw any) {
		for _, field := range normalizeContractFields(raw) {
			field.EndpointID, field.Service, field.Method, field.Path = id, service, strings.ToUpper(method), path
			if field.Location == "" {
				field.Location = location
			}
			if field.Source == "" {
				field.Source = source
			}
			field.Evidence = d["locations"]
			out = append(out, field)
		}
	}
	add("body", "declared_or_static", d["request_fields"])
	add("body", "static", d["body_fields"])
	add("query", "static", d["query_params"])
	add("path", "route", d["path_params"])
	add("header", "static", d["headers"])
	add("input", "declared_or_static", d["inputs"])
	return out
}

// contractDetails presents normalized graph fields and the original detector
// details as one view. Graph normalization deliberately retains detector-specific
// contract evidence under metadata.details, while query callers should not need
// to know which representation supplied a field.
func contractDetails(details map[string]any) map[string]any {
	merged := map[string]any{}
	if metadata, ok := details["metadata"].(map[string]any); ok {
		if original, ok := metadata["details"].(map[string]any); ok {
			for key, value := range original {
				merged[key] = value
			}
		}
	}
	for key, value := range details {
		merged[key] = value
	}
	return merged
}

func normalizeContractFields(raw any) []ContractField {
	var out []ContractField
	switch value := raw.(type) {
	case []any:
		for _, item := range value {
			out = append(out, normalizeContractFields(item)...)
		}
	case map[string]any:
		if name := stringValue(value["name"]); name != "" {
			out = append(out, ContractField{Name: name, Type: stringValue(value["type"]), Required: boolValue(value["required"]), Nullable: boolValue(value["nullable"]), SchemaPointer: stringValue(value["schema_pointer"]), Location: stringValue(value["location"]), Source: stringValue(value["source"]), SourceFile: stringValue(value["source_file"]), SourceLine: intValue(value["source_line"])})
		} else {
			keys := make([]string, 0, len(value))
			for key := range value {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				field := ContractField{Name: key}
				if spec, ok := value[key].(map[string]any); ok {
					field.Type = stringValue(spec["type"])
					field.Required = boolValue(spec["required"])
					field.Nullable = boolValue(spec["nullable"])
				} else {
					field.Type = stringValue(value[key])
				}
				out = append(out, field)
			}
		}
	case string:
		if strings.TrimSpace(value) != "" {
			out = append(out, ContractField{Name: strings.TrimSpace(value)})
		}
	}
	return out
}

func contractFieldKey(field ContractField) string {
	return strings.Join([]string{field.Service, field.EndpointID, field.Location, field.Name}, "\x00")
}
func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
func boolValue(value any) bool { result, _ := value.(bool); return result }
func intValue(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case float64:
		return int(number)
	}
	return 0
}
