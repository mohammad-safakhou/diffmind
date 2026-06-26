package discovery

import (
	"sort"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
)

// DeterministicSAMQueueConsumers emits event-source consumers from SAM /
// CloudFormation templates. The first supported source is DynamoDB Streams,
// which is a queue-like service entrypoint in the DiffMind protocol graph.
func DeterministicSAMQueueConsumers(idx *astpkg.ProjectIndex) []llmEntity {
	if idx == nil {
		return nil
	}
	paths := sortedConfigPaths(idx)
	var out []llmEntity
	seen := map[string]struct{}{}
	for _, path := range paths {
		cf := idx.Configs[path]
		if cf == nil || !looksLikeSAMTemplate(path, cf) {
			continue
		}
		for _, ev := range samEventSources(cf) {
			key := strings.ToLower(ev.Platform + "|" + ev.Destination + "|" + ev.Handler)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			line := ev.Line
			if line <= 0 {
				line = 1
			}
			details := map[string]any{
				"platform":          ev.Platform,
				"event_source":      ev.Source,
				"event_source_type": ev.Type,
				"discovered_by":     "aws_sam_event_source",
			}
			if ev.Handler != "" {
				details["handler"] = ev.Handler
			}
			if ev.StreamArn != "" {
				details["stream_arn"] = ev.StreamArn
			}
			if ev.Destination != "" {
				details["stream"] = ev.Destination
				details["table"] = ev.Destination
			}
			out = append(out, llmEntity{
				Type:       "queue_consumer",
				Name:       ev.Destination,
				Summary:    "AWS SAM DynamoDB stream event source detected from template",
				Confidence: 1.0,
				Tags:       []string{"deterministic", "aws-sam", "dynamodb-stream"},
				Details:    details,
				Locations: []llmLocation{{
					File:      path,
					StartLine: line,
					EndLine:   line,
				}},
				Evidence: []llmEvidence{{
					File:      path,
					StartLine: line,
					EndLine:   line,
					Snippet:   ev.Source,
					Source:    "deterministic_config",
				}},
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return firstLocationFile(out[i]) < firstLocationFile(out[j])
	})
	return out
}

type samEventSource struct {
	Type        string
	Platform    string
	Destination string
	StreamArn   string
	Handler     string
	Source      string
	Line        int
}

func looksLikeSAMTemplate(path string, cf *astpkg.ConfigFile) bool {
	p := strings.ToLower(strings.TrimSpace(path))
	if strings.HasSuffix(p, "template.yaml") || strings.HasSuffix(p, "template.yml") || strings.Contains(p, "sam") {
		return true
	}
	for _, e := range cf.Entries {
		if strings.EqualFold(e.Key, "Transform") && strings.Contains(strings.ToLower(e.Value), "serverless") {
			return true
		}
		if strings.HasSuffix(strings.ToLower(e.Key), ".type") && strings.EqualFold(strings.TrimSpace(e.Value), "AWS::Serverless::Function") {
			return true
		}
	}
	return false
}

func samEventSources(cf *astpkg.ConfigFile) []samEventSource {
	type functionInfo struct {
		isFunction bool
		handler    string
	}
	functions := map[string]*functionInfo{}
	for _, e := range cf.Entries {
		parts := strings.Split(e.Key, ".")
		if len(parts) < 3 || parts[0] != "Resources" {
			continue
		}
		resource := parts[1]
		info := functions[resource]
		if info == nil {
			info = &functionInfo{}
			functions[resource] = info
		}
		if len(parts) == 3 && parts[2] == "Type" && strings.EqualFold(strings.TrimSpace(e.Value), "AWS::Serverless::Function") {
			info.isFunction = true
		}
		if strings.Join(parts[2:], ".") == "Properties.Handler" {
			info.handler = strings.Trim(strings.TrimSpace(e.Value), `"'`)
		}
	}

	var out []samEventSource
	for _, e := range cf.Entries {
		parts := strings.Split(e.Key, ".")
		if len(parts) < 6 || parts[0] != "Resources" || parts[2] != "Properties" || parts[3] != "Events" || parts[5] != "Type" {
			continue
		}
		resource, eventName := parts[1], parts[4]
		info := functions[resource]
		if info == nil || !info.isFunction {
			continue
		}
		eventType := strings.TrimSpace(e.Value)
		if !strings.EqualFold(eventType, "DynamoDB") {
			continue
		}
		prefix := strings.Join(parts[:5], ".") + ".Properties"
		streamArn, table := samEventStream(cf, prefix, eventName)
		if table == "" {
			table = normalizeResourceToken(eventName)
		}
		if table == "" {
			continue
		}
		out = append(out, samEventSource{
			Type:        eventType,
			Platform:    "dynamodb_stream",
			Destination: table,
			StreamArn:   streamArn,
			Handler:     info.handler,
			Source:      "DynamoDB stream " + table,
			Line:        e.Line,
		})
	}
	return out
}

func samEventStream(cf *astpkg.ConfigFile, prefix, eventName string) (streamArn, table string) {
	for _, e := range cf.Entries {
		if !strings.HasPrefix(e.Key, prefix+".") {
			continue
		}
		if !strings.Contains(strings.ToLower(e.Key), "stream") {
			continue
		}
		value := strings.Trim(strings.TrimSpace(e.Value), `"'`)
		if value == "" {
			continue
		}
		if streamArn == "" && (strings.Contains(value, "arn:") || strings.Contains(strings.ToLower(value), "stream")) {
			streamArn = value
		}
		if t := dynamoStreamTableName(value); t != "" {
			return streamArn, t
		}
	}
	return streamArn, dynamoStreamTableName(eventName)
}

func dynamoStreamTableName(raw string) string {
	s := strings.Trim(strings.TrimSpace(raw), `"'`)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "table/"); i >= 0 {
		table := s[i+len("table/"):]
		if j := strings.Index(table, "/"); j >= 0 {
			table = table[:j]
		}
		return normalizeResourceToken(table)
	}
	lower := strings.ToLower(s)
	for _, suffix := range []string{"-dynamodb-stream-arn", "-dynamodb-stream", "-stream-arn", "-stream"} {
		if strings.Contains(lower, suffix) {
			cleaned := stripTemplateReferences(s)
			cleaned = strings.TrimSuffix(cleaned, suffix)
			cleaned = strings.TrimSuffix(cleaned, strings.ReplaceAll(suffix, "-", "_"))
			return normalizeResourceToken(cleaned)
		}
	}
	return ""
}

func stripTemplateReferences(raw string) string {
	var b strings.Builder
	for i := 0; i < len(raw); {
		if i+1 < len(raw) && raw[i] == '$' && raw[i+1] == '{' {
			if end := strings.IndexByte(raw[i+2:], '}'); end >= 0 {
				i += end + 3
				continue
			}
		}
		b.WriteByte(raw[i])
		i++
	}
	return b.String()
}

func normalizeResourceToken(raw string) string {
	s := strings.TrimSpace(stripTemplateReferences(raw))
	s = strings.Trim(s, `"'/:`)
	s = strings.NewReplacer("_", "-", " ", "-").Replace(s)
	s = strings.Trim(s, "-")
	if s == "" {
		return ""
	}
	return strings.ToLower(s)
}

func firstLocationFile(e llmEntity) string {
	if len(e.Locations) == 0 {
		return ""
	}
	return e.Locations[0].File
}
