package discovery

import (
	"fmt"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/util"
)

// clients.go converts connection_client discovery Candidates into the typed
// model.ConnectionClient backbone records consumed by deterministic instance
// propagation (see client_propagation.go). It is pure (no I/O) so it is trivially
// testable.

// ClientsFromCandidates maps connection_client discovery items to
// model.ConnectionClient. LogicalName is the candidate name; kind/symbol/
// framework/config_anchor come from details. Items without a usable name are
// dropped.
func ClientsFromCandidates(items []extraction.Candidate) []model.ConnectionClient {
	out := make([]model.ConnectionClient, 0, len(items))
	for _, it := range items {
		name := strings.TrimSpace(it.Name)
		if name == "" {
			continue
		}
		c := model.ConnectionClient{
			LogicalName:  name,
			Kind:         NormalizeClientKind(clientDetail(it.Details, "kind")),
			Symbol:       clientDetail(it.Details, "symbol", "client_type", "type", "class"),
			Framework:    clientDetail(it.Details, "framework"),
			ConfigAnchor: clientDetail(it.Details, "config_anchor", "config_key", "property", "config_property"),
			Source:       "deterministic",
		}
		for _, l := range it.Locations {
			if strings.TrimSpace(l.File) == "" {
				continue
			}
			c.Locations = append(c.Locations, model.Location{File: l.File, StartLine: l.StartLine, EndLine: l.EndLine})
		}
		file := ""
		if len(c.Locations) > 0 {
			file = c.Locations[0].File
		}
		c.ID = util.StableID(string(model.KindClient), c.Kind, strings.ToLower(c.LogicalName), file)
		out = append(out, c)
	}
	return out
}

// NormalizeClientKind folds free-text client kind values into the canonical
// db|http|queue|cache|stream set the propagation pass keys on. Unrecognised
// values pass through lower-cased so a new kind is never silently lost.
func NormalizeClientKind(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case s == "":
		return ""
	case strings.Contains(s, "stream") || strings.Contains(s, "kinesis"):
		return "stream"
	case strings.Contains(s, "cache") || strings.Contains(s, "redis") || strings.Contains(s, "memcache"):
		return "cache"
	case strings.Contains(s, "queue") || strings.Contains(s, "sqs") || strings.Contains(s, "sns") ||
		strings.Contains(s, "kafka") || strings.Contains(s, "rabbit") || strings.Contains(s, "messaging") ||
		strings.Contains(s, "broker") || strings.Contains(s, "topic"):
		return "queue"
	case strings.Contains(s, "http") || strings.Contains(s, "rest") || strings.Contains(s, "feign") ||
		strings.Contains(s, "web") || strings.Contains(s, "api") || strings.Contains(s, "rpc") ||
		strings.Contains(s, "grpc"):
		return "http"
	case strings.Contains(s, "db") || strings.Contains(s, "data") || strings.Contains(s, "sql") ||
		strings.Contains(s, "orm") || strings.Contains(s, "repository") || strings.Contains(s, "jpa") ||
		strings.Contains(s, "mongo") || strings.Contains(s, "dynamo"):
		return "db"
	}
	return s
}

// clientDetail returns the first non-empty scalar detail value for the given keys.
func clientDetail(d map[string]any, keys ...string) string {
	if d == nil {
		return ""
	}
	for _, k := range keys {
		v, ok := d[k]
		if !ok || v == nil {
			continue
		}
		s := strings.TrimSpace(fmt.Sprint(v))
		if s != "" && s != "<nil>" {
			return s
		}
	}
	return ""
}
