package archgraph

import (
	"encoding/json"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/model"
)

// Supplement is evaluated once during collection and incorporated into the
// immutable graph snapshot, never re-evaluated while reading a historical run.
type Supplement struct {
	Dependencies []model.Dependency
	Exposures    []model.Exposure
	Targets      map[string]ResolvedTarget // dependency ID -> authoritative resolver match
}

type ResolvedTarget struct {
	Service    string
	Reason     string
	Confidence float64
}

func supplementData(exposures, dependencies map[string][]map[string]any, supplement Supplement) {
	for _, exp := range supplement.Exposures {
		exposures[exp.Type] = append(exposures[exp.Type], entityMap(exp.BaseEntity))
	}
	for _, dep := range supplement.Dependencies {
		dependencies[dep.Type] = append(dependencies[dep.Type], entityMap(dep.BaseEntity))
	}
	for _, kind := range []string{"outbound_http", "outbound_rpc"} {
		for _, item := range dependencies[kind] {
			if match, ok := supplement.Targets[getString(item, "id")]; ok {
				details := getMap(item, "details")
				if details == nil {
					details = map[string]any{}
					item["details"] = details
				}
				details["target_service"] = match.Service
				details["resolution_reason"] = match.Reason
				details["resolution_confidence"] = match.Confidence
			}
		}
	}
}

func entityMap(entity model.BaseEntity) map[string]any {
	body, _ := json.Marshal(entity)
	var item map[string]any
	_ = json.Unmarshal(body, &item)
	return item
}
