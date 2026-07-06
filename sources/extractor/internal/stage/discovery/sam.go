package discovery

import (
	"sort"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
)

// DeterministicSAMQueueConsumers emits event-source consumers from SAM /
// CloudFormation templates. DynamoDB Streams and SQS event sources are
// queue-like service entrypoints in the DiffMind protocol graph.
func DeterministicSAMQueueConsumers(idx *astpkg.ProjectIndex) []candidate {
	if idx == nil {
		return nil
	}
	paths := sortedConfigPaths(idx)
	var out []candidate
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
			if ev.SourceArn != "" {
				details["source_arn"] = ev.SourceArn
			}
			if ev.Destination != "" {
				details["destination"] = ev.Destination
				switch ev.Platform {
				case "dynamodb_stream":
					details["stream"] = ev.Destination
					details["table"] = ev.Destination
				case "sqs":
					details["queue"] = ev.Destination
				}
			}
			summary := "AWS SAM event source detected from template"
			tags := []string{"deterministic", "aws-sam", ev.Platform}
			out = append(out, candidate{
				Type:       "queue_consumer",
				Name:       ev.Destination,
				Summary:    summary,
				Confidence: 1.0,
				Tags:       tags,
				Details:    details,
				Locations: []candidateLocation{{
					File:      path,
					StartLine: line,
					EndLine:   line,
				}},
				Evidence: []candidateEvidence{{
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
	SourceArn   string
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
		if !strings.EqualFold(eventType, "DynamoDB") && !strings.EqualFold(eventType, "SQS") {
			continue
		}
		prefix := strings.Join(parts[:5], ".") + ".Properties"
		sourceArn, destination := samEventDestination(cf, prefix, eventName, eventType)
		if destination == "" {
			destination = normalizeResourceToken(eventName)
		}
		if destination == "" {
			continue
		}
		platform := "dynamodb_stream"
		source := "DynamoDB stream " + destination
		if strings.EqualFold(eventType, "SQS") {
			platform = "sqs"
			source = "SQS queue " + destination
		}
		out = append(out, samEventSource{
			Type:        eventType,
			Platform:    platform,
			Destination: destination,
			SourceArn:   sourceArn,
			Handler:     info.handler,
			Source:      source,
			Line:        e.Line,
		})
	}
	return out
}

func samEventDestination(cf *astpkg.ConfigFile, prefix, eventName, eventType string) (sourceArn, destination string) {
	if strings.EqualFold(eventType, "SQS") {
		return samEventQueue(cf, prefix, eventName)
	}
	return samEventStream(cf, prefix, eventName)
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

func samEventQueue(cf *astpkg.ConfigFile, prefix, eventName string) (queueArn, queue string) {
	for _, e := range cf.Entries {
		if !strings.HasPrefix(e.Key, prefix+".") {
			continue
		}
		lowerKey := strings.ToLower(e.Key)
		if !strings.Contains(lowerKey, "queue") && !strings.Contains(lowerKey, "arn") {
			continue
		}
		value := strings.Trim(strings.TrimSpace(e.Value), `"'`)
		if value == "" {
			continue
		}
		if queueArn == "" && (strings.Contains(value, "arn:") || strings.Contains(strings.ToLower(value), "queue")) {
			queueArn = value
		}
		if q := sqsQueueName(value); q != "" {
			return queueArn, q
		}
	}
	return queueArn, sqsQueueName(eventName)
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

func sqsQueueName(raw string) string {
	s := strings.Trim(strings.TrimSpace(raw), `"'`)
	if s == "" {
		return ""
	}
	if i := strings.LastIndex(s, ":"); i >= 0 && i < len(s)-1 && strings.Contains(s[:i], "arn:") {
		return normalizeResourceToken(s[i+1:])
	}
	lower := strings.ToLower(s)
	for _, suffix := range []string{"-queue-arn", "-queue-url", "-queue-name", "-sqs-arn", "-sqs-url", "-sqs-name"} {
		if strings.Contains(lower, suffix) {
			cleaned := stripTemplateReferences(s)
			cleaned = strings.TrimSuffix(cleaned, suffix)
			cleaned = strings.TrimSuffix(cleaned, strings.ReplaceAll(suffix, "-", "_"))
			return normalizeResourceToken(cleaned)
		}
	}
	if strings.Contains(lower, "queue") || strings.Contains(lower, "sqs") {
		return normalizeResourceToken(s)
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

func firstLocationFile(e candidate) string {
	if len(e.Locations) == 0 {
		return ""
	}
	return e.Locations[0].File
}
