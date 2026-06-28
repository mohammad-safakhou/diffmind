package provenance

import (
	"fmt"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// NormalizeDeterministic replaces empty provenance from deterministic
// candidates with detector-specific provenance.
func NormalizeDeterministic(exposures []model.Exposure, dependencies []model.Dependency, connections []model.Connection) {
	for i := range exposures {
		normalizeBase(&exposures[i].BaseEntity)
	}
	for i := range dependencies {
		normalizeBase(&dependencies[i].BaseEntity)
	}
	for i := range connections {
		if strings.TrimSpace(connections[i].Source) == "" {
			connections[i].Source = model.ConnectionSourceAST
		}
		for j := range connections[i].Evidence {
			if isPlaceholderSource(connections[i].Evidence[j].Source) {
				connections[i].Evidence[j].Source = "deterministic.connections"
			}
		}
		for j := range connections[i].Paths {
			for k := range connections[i].Paths[j].Steps {
				for l := range connections[i].Paths[j].Steps[k].Evidence {
					if isPlaceholderSource(connections[i].Paths[j].Steps[k].Evidence[l].Source) {
						connections[i].Paths[j].Steps[k].Evidence[l].Source = "deterministic.connections"
					}
				}
			}
		}
	}
}

func normalizeBase(base *model.BaseEntity) {
	source := deterministicSource(*base)
	if source == "" {
		source = "deterministic"
	}
	if isPlaceholderSource(base.PluginSource) {
		base.PluginSource = source
	}
	for i := range base.Evidence {
		if isPlaceholderSource(base.Evidence[i].Source) {
			base.Evidence[i].Source = source
		}
	}
}

func deterministicSource(base model.BaseEntity) string {
	for _, key := range []string{"discovered_by", "detector", "source", "source_detector"} {
		if v := detailString(base.Details, key); v != "" && !isPlaceholderSource(v) {
			return v
		}
	}
	for _, tag := range base.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if v, ok := strings.CutPrefix(tag, "framework:"); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		if strings.HasPrefix(tag, "deterministic_") || strings.HasPrefix(tag, "ast_") {
			return tag
		}
	}
	for _, ev := range base.Evidence {
		if s := strings.TrimSpace(ev.Source); s != "" && !isPlaceholderSource(s) {
			return s
		}
	}
	switch base.Type {
	case "http_route", "webhook":
		return "deterministic.http"
	case "outbound_http":
		return "deterministic.http_client"
	case "db_operation":
		return "deterministic.db"
	case "cache_operation":
		return "deterministic.cache"
	case "queue_consumer", "queue_publish":
		return "deterministic.queue"
	case "cli_command":
		return "deterministic.cli"
	case "scheduled_job":
		return "deterministic.activation"
	default:
		if base.Type != "" {
			return fmt.Sprintf("deterministic.%s", strings.ReplaceAll(base.Type, " ", "_"))
		}
		return "deterministic"
	}
}

func detailString(details map[string]any, key string) string {
	if details == nil {
		return ""
	}
	v, ok := details[key]
	if !ok {
		return ""
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "" || s == "<nil>" {
		return ""
	}
	return s
}

func isPlaceholderSource(source string) bool {
	source = strings.ToLower(strings.TrimSpace(source))
	return source == ""
}
