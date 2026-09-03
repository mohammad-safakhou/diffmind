// Package resolver implements deterministic cross-service identity resolution.
// It takes the service registry (with architecture + identity data) and matches
// each service's outbound dependencies to known service identities using
// pack-derived aliases and resource identifiers.
//
// Resolution is intentionally deterministic only: there is no fallback or
// external model dependency. The same inputs always produce the same graph.
package resolver

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/knowledge"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/model"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/registry"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
)

// Resolver performs cross-service identity resolution.
type Resolver struct {
	registry *registry.Registry
	log      *util.Logger
	rules    []knowledge.ResolutionRule
}

// New creates a new deterministic resolver.
func New(reg *registry.Registry, log *util.Logger, rules ...knowledge.ResolutionRule) *Resolver {
	return &Resolver{registry: reg, log: log, rules: append([]knowledge.ResolutionRule(nil), rules...)}
}

// ResolvedMatch represents a single dependency → service match.
type ResolvedMatch struct {
	FromService    string  `json:"from_service"`
	DependencyID   string  `json:"dependency_id"`
	DependencyName string  `json:"dependency_name"`
	DependencyType string  `json:"dependency_type"`
	ToService      string  `json:"to_service"`
	MatchType      string  `json:"match_type"` // http, queue, rpc, shared_db
	Confidence     float64 `json:"confidence"`
	Reasoning      string  `json:"reasoning"`
}

// Resolution holds the full resolution output.
type Resolution struct {
	Matches    []ResolvedMatch        `json:"matches"`
	Unresolved []model.UnresolvedEdge `json:"unresolved"`
}

// Resolve performs deterministic identity resolution across all services.
// Dependencies that do not match any known identity are returned as unresolved
// edges (never guessed).
func (r *Resolver) Resolve() (*Resolution, error) {
	result := &Resolution{}

	entries := r.registry.AllWithArchitecture()
	if len(entries) == 0 {
		return result, nil
	}

	// Build identity index for deterministic matching.
	identityIndex := r.buildIdentityIndex()

	// For each service, try to match its outbound dependencies to known identities.
	for _, entry := range entries {
		if entry.Architecture == nil {
			continue
		}
		for _, dep := range entry.Architecture.Dependencies {
			match, err := r.tryDeterministicMatch(entry.Name, &dep, identityIndex)
			if err != nil {
				return nil, err
			}
			if match != nil {
				result.Matches = append(result.Matches, *match)
			} else {
				result.Unresolved = append(result.Unresolved, model.UnresolvedEdge{
					Service:        entry.Name,
					DependencyID:   dep.ID,
					DependencyName: dep.Name,
					Type:           dep.Type,
					Target:         extractTarget(&dep),
					Reason:         "no_deterministic_match",
				})
			}
		}
	}

	return result, nil
}

// identityEntry is an index entry for quick lookup.
type identityEntry struct {
	ServiceName string
	Kind        string // dns, iam_role, queue, etc.
	Value       string
	Normalized  string
}

func (r *Resolver) buildIdentityIndex() []identityEntry {
	var index []identityEntry
	for _, entry := range r.registry.All() {
		if entry.Name != "" {
			index = append(index, identityEntry{
				ServiceName: entry.Name,
				Kind:        "service_name",
				Value:       strings.ToLower(entry.Name),
				Normalized:  normalizeIdentity(entry.Name),
			})
		}
		if entry.Identity == nil {
			if entry.Architecture != nil {
				index = append(index, queueExposureIdentities(entry)...)
			}
			continue
		}
		for _, alias := range entry.Identity.Aliases {
			index = append(index, identityEntry{
				ServiceName: entry.Name,
				Kind:        alias.Kind,
				Value:       strings.ToLower(alias.Value),
				Normalized:  normalizeIdentity(alias.Value),
			})
		}
		for _, res := range entry.Identity.Resources {
			kind := res.Kind
			normalized := normalizeIdentity(res.Identifier)
			if strings.Contains(strings.ToLower(kind), "queue") || strings.Contains(strings.ToLower(kind), "topic") {
				kind = "queue_topic"
				normalized = normalizeQueueTopic(res.Identifier)
			}
			index = append(index, identityEntry{
				ServiceName: entry.Name,
				Kind:        kind,
				Value:       strings.ToLower(res.Identifier),
				Normalized:  normalized,
			})
		}
		if entry.Architecture != nil {
			index = append(index, queueExposureIdentities(entry)...)
		}
	}
	return index
}

func (r *Resolver) tryDeterministicMatch(fromService string, dep *model.Dependency, index []identityEntry) (*ResolvedMatch, error) {
	targetRaw := extractTarget(dep)
	target := strings.ToLower(targetRaw)
	if match, err := r.tryKnowledgeRule(fromService, dep, targetRaw); match != nil || err != nil {
		return match, err
	}
	if isHTTPDependency(dep.Type) {
		if match := r.tryHTTPExposureMatch(fromService, dep); match != nil {
			return match, nil
		}
	}
	if target == "" {
		return nil, nil
	}

	if isQueuePublish(dep.Type) {
		topic := normalizeQueueTopic(targetRaw)
		if topic != "" {
			for _, entry := range index {
				if entry.ServiceName == fromService || entry.Kind != "queue_topic" {
					continue
				}
				if topic == entry.Normalized {
					return &ResolvedMatch{
						FromService:    fromService,
						DependencyID:   dep.ID,
						DependencyName: dep.Name,
						DependencyType: dep.Type,
						ToService:      entry.ServiceName,
						MatchType:      "queue",
						Confidence:     0.9,
						Reasoning:      fmt.Sprintf("deterministic queue topic match: %q equals %q", topic, entry.Value),
					}, nil
				}
			}
		}
	}

	targetNorm := normalizeIdentity(targetRaw)
	targetHost := normalizeHostname(targetRaw)
	var best *ResolvedMatch
	ambiguous := false
	for _, entry := range index {
		if entry.ServiceName == fromService {
			continue // skip self-references
		}
		confidence, reason, ok := matchIdentityTier(target, targetNorm, targetHost, entry)
		if ok && (best == nil || confidence > best.Confidence) {
			ambiguous = false
			best = &ResolvedMatch{
				FromService:    fromService,
				DependencyID:   dep.ID,
				DependencyName: dep.Name,
				DependencyType: dep.Type,
				ToService:      entry.ServiceName,
				MatchType:      classifyMatchType(dep.Type),
				Confidence:     confidence,
				Reasoning:      reason,
			}
		} else if ok && confidence == best.Confidence && entry.ServiceName != best.ToService {
			ambiguous = true
		}
	}
	if ambiguous {
		return nil, fmt.Errorf("ambiguous service identity for dependency %s in %s: multiple services match %q equally; add an explicit resolution rule", dep.ID, fromService, targetRaw)
	}
	return best, nil
}

func (r *Resolver) tryKnowledgeRule(fromService string, dep *model.Dependency, target string) (*ResolvedMatch, error) {
	type candidate struct {
		rule    knowledge.ResolutionRule
		service string
	}
	var candidates []candidate
	for _, rule := range r.rules {
		if rule.DependencyType != "" && rule.DependencyType != "*" {
			matched, err := filepath.Match(rule.DependencyType, dep.Type)
			if err != nil || !matched {
				continue
			}
		}
		pattern, err := regexp.Compile(rule.TargetPattern)
		if err != nil {
			return nil, fmt.Errorf("knowledge pack %s rule %s: %w", rule.PackID, rule.Name, err)
		}
		match := pattern.FindStringSubmatchIndex(target)
		if match == nil {
			continue
		}
		service := string(pattern.ExpandString(nil, rule.TargetService, target, match))
		service = strings.TrimSpace(service)
		if service == "" || service == fromService || r.registry.Get(service) == nil {
			continue
		}
		candidates = append(candidates, candidate{rule: rule, service: service})
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	selected := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.rule.PackPriority > selected.rule.PackPriority {
			selected = candidate
		}
	}
	highest := selected.rule.PackPriority
	for _, candidate := range candidates {
		if candidate.rule.PackPriority != highest {
			continue
		}
		if candidate.service != selected.service {
			return nil, fmt.Errorf("knowledge pack resolution conflict: %s/%s and %s/%s have priority %d but resolve %q to different services %q and %q",
				selected.rule.PackID, selected.rule.Name, candidate.rule.PackID, candidate.rule.Name,
				highest, target, selected.service, candidate.service)
		}
	}
	return &ResolvedMatch{
		FromService: fromService, DependencyID: dep.ID, DependencyName: dep.Name,
		DependencyType: dep.Type, ToService: selected.service,
		MatchType: classifyMatchType(dep.Type), Confidence: selected.rule.Confidence,
		Reasoning: fmt.Sprintf("knowledge pack %s rule %s matched target %q", selected.rule.PackID, selected.rule.Name, target),
	}, nil
}

func (r *Resolver) tryHTTPExposureMatch(fromService string, dep *model.Dependency) *ResolvedMatch {
	depMethod, depPath := httpRouteFromDependency(dep)
	if depPath == "" {
		return nil
	}
	exact := r.findHTTPRouteCandidates(fromService, depMethod, depPath, false)
	if len(exact) == 1 {
		return httpExposureResolvedMatch(fromService, dep, exact[0], 0.92, "deterministic exact HTTP exposure route match")
	}
	if len(exact) > 1 {
		return nil
	}
	stripped := r.findHTTPRouteCandidates(fromService, depMethod, depPath, true)
	if len(stripped) == 1 {
		return httpExposureResolvedMatch(fromService, dep, stripped[0], 0.84, "deterministic normalized HTTP exposure route match")
	}
	return nil
}

type httpRouteCandidate struct {
	ServiceName string
	ExposureID  string
	Method      string
	Path        string
}

func (r *Resolver) findHTTPRouteCandidates(fromService, depMethod, depPath string, stripPrefixes bool) []httpRouteCandidate {
	depNorm := normalizeHTTPPath(depPath, stripPrefixes)
	if depNorm == "" {
		return nil
	}
	depMethod = strings.ToUpper(strings.TrimSpace(depMethod))
	var out []httpRouteCandidate
	for _, entry := range r.registry.AllWithArchitecture() {
		if entry.Name == fromService || entry.Architecture == nil {
			continue
		}
		for _, exp := range entry.Architecture.Exposures {
			if !isHTTPExposure(exp.Type) {
				continue
			}
			method, path := httpRouteFromExposure(&exp)
			if path == "" {
				continue
			}
			if depMethod != "" && method != "" && depMethod != method {
				continue
			}
			if depMethod != "" && method == "" {
				continue
			}
			if normalizeHTTPPath(path, stripPrefixes) != depNorm {
				continue
			}
			out = append(out, httpRouteCandidate{
				ServiceName: entry.Name,
				ExposureID:  exp.ID,
				Method:      method,
				Path:        path,
			})
		}
	}
	return out
}

func httpExposureResolvedMatch(fromService string, dep *model.Dependency, candidate httpRouteCandidate, confidence float64, prefix string) *ResolvedMatch {
	return &ResolvedMatch{
		FromService:    fromService,
		DependencyID:   dep.ID,
		DependencyName: dep.Name,
		DependencyType: dep.Type,
		ToService:      candidate.ServiceName,
		MatchType:      "http",
		Confidence:     confidence,
		Reasoning:      fmt.Sprintf("%s: dependency route matches %s %s on exposure %s", prefix, candidate.Method, candidate.Path, candidate.ExposureID),
	}
}

func queueExposureIdentities(entry *registry.ServiceEntry) []identityEntry {
	if entry == nil || entry.Architecture == nil {
		return nil
	}
	var out []identityEntry
	for _, exp := range entry.Architecture.Exposures {
		if exp.Type != "queue_consumer" && exp.Type != "stream_consume" {
			continue
		}
		for _, raw := range queueTargets(exp.Instance, exp.Details) {
			norm := normalizeQueueTopic(raw)
			if norm == "" {
				continue
			}
			out = append(out, identityEntry{
				ServiceName: entry.Name,
				Kind:        "queue_topic",
				Value:       strings.ToLower(raw),
				Normalized:  norm,
			})
		}
	}
	return out
}

func queueTargets(instance string, details map[string]any) []string {
	var out []string
	if isUsefulTarget(instance) {
		out = append(out, instance)
	}
	for _, key := range []string{"topic", "queue", "queue_name", "destination", "queue_url"} {
		if v, ok := details[key]; ok {
			if s := detailString(v); isUsefulTarget(s) {
				out = append(out, s)
			}
		}
	}
	return out
}

func matchIdentityTier(target, targetNorm, targetHost string, entry identityEntry) (float64, string, bool) {
	if targetNorm != "" && entry.Normalized != "" && targetNorm == entry.Normalized {
		return 0.95, fmt.Sprintf("deterministic exact identity match: target %q equals identity %q (%s)", target, entry.Value, entry.Kind), true
	}
	if targetHost != "" {
		entryHost := normalizeHostname(entry.Value)
		if entryHost != "" && targetHost == entryHost {
			return 0.9, fmt.Sprintf("deterministic hostname match: target host %q equals identity host %q (%s)", targetHost, entryHost, entry.Kind), true
		}
	}
	if tokenBoundaryContains(target, entry.Value) || tokenBoundaryContains(entry.Value, target) {
		return 0.75, fmt.Sprintf("deterministic token-boundary match: target %q matches identity %q (%s)", target, entry.Value, entry.Kind), true
	}
	return 0, "", false
}

// extractTarget tries to pull a meaningful target identifier from a dependency.
func extractTarget(dep *model.Dependency) string {
	for _, v := range []string{dep.Instance} {
		if isUsefulTarget(v) {
			return strings.TrimSpace(v)
		}
	}
	// Check details map for common keys.
	if dep.Details != nil {
		for _, key := range []string{
			"url", "base_url", "target_url", "default_url", "production_url",
			"host", "target_host", "target_service", "service",
			"queue", "queue_name", "destination", "topic",
			"database_name", "database", "table_or_entity", "table", "entity", "instance",
		} {
			if v, ok := dep.Details[key]; ok {
				if s := detailString(v); isUsefulTarget(s) {
					return s
				}
			}
		}
	}
	// Check tags for service-like identifiers.
	for _, tag := range dep.Tags {
		if strings.Contains(tag, "-api") || strings.Contains(tag, "-service") || strings.Contains(tag, ".") {
			return tag
		}
	}
	// Fall back to evidence/summary for URL-like patterns.
	for _, ev := range dep.Evidence {
		if strings.Contains(ev.Snippet, "://") || strings.Contains(ev.Snippet, ".internal") || strings.Contains(ev.Snippet, ".global") {
			// Extract URL-like pattern from snippet.
			for _, word := range strings.Fields(ev.Snippet) {
				word = strings.Trim(word, `"'(){},;`)
				if strings.Contains(word, "://") || strings.Contains(word, ".internal") || strings.Contains(word, ".global") {
					return word
				}
			}
		}
	}
	return dep.Name
}

func detailString(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func isUsefulTarget(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "unknown", "http", "rpc", "database", "cache", "queue", "kafka", "sqs", "sns", "postgres", "postgresql", "mysql", "redis", "dynamodb", "mongodb":
		return false
	}
	return true
}

func classifyMatchType(depType string) string {
	switch {
	case strings.Contains(depType, "http"):
		return "http"
	case strings.Contains(depType, "rpc") || strings.Contains(depType, "grpc"):
		return "rpc"
	case strings.Contains(depType, "queue") || strings.Contains(depType, "publish"):
		return "queue"
	case strings.Contains(depType, "db") || strings.Contains(depType, "database"):
		return "shared_db"
	default:
		return depType
	}
}

func isHTTPDependency(depType string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(depType)), "http")
}

func isHTTPExposure(expType string) bool {
	expType = strings.ToLower(strings.TrimSpace(expType))
	return expType == "http_route" || strings.Contains(expType, "http")
}

func httpRouteFromDependency(dep *model.Dependency) (method, path string) {
	if dep == nil {
		return "", ""
	}
	if dep.Details != nil {
		method = strings.ToUpper(detailString(dep.Details["method"]))
		for _, key := range []string{"path", "url_template", "endpoint", "url"} {
			if s := detailString(dep.Details[key]); s != "" {
				path = routePathOnly(s)
				break
			}
		}
	}
	if method == "" || path == "" {
		nameMethod, namePath := parseHTTPRouteText(dep.Name)
		if method == "" {
			method = nameMethod
		}
		if path == "" {
			path = namePath
		}
	}
	if path == "" {
		if target := extractTarget(dep); target != dep.Name {
			_, path = parseHTTPRouteText(target)
			if path == "" {
				path = routePathOnly(target)
			}
		}
	}
	return method, path
}

func httpRouteFromExposure(exp *model.Exposure) (method, path string) {
	if exp == nil {
		return "", ""
	}
	if exp.Details != nil {
		method = strings.ToUpper(detailString(exp.Details["method"]))
		for _, key := range []string{"path", "url_template", "route", "endpoint", "url"} {
			if s := detailString(exp.Details[key]); s != "" {
				path = routePathOnly(s)
				break
			}
		}
	}
	if method == "" || path == "" {
		nameMethod, namePath := parseHTTPRouteText(exp.Name)
		if method == "" {
			method = nameMethod
		}
		if path == "" {
			path = namePath
		}
	}
	return method, path
}

func parseHTTPRouteText(raw string) (method, path string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	re := regexp.MustCompile(`(?i)\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+([^\s]+)`)
	if m := re.FindStringSubmatch(raw); len(m) > 2 {
		return strings.ToUpper(m[1]), routePathOnly(m[2])
	}
	return "", ""
}

func routePathOnly(raw string) string {
	raw = strings.TrimSpace(strings.Trim(raw, `"'`))
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		parseable := regexp.MustCompile(`\$\{[^}]+\}`).ReplaceAllString(raw, "placeholder")
		if u, err := url.Parse(parseable); err == nil && u.Path != "" {
			raw = u.Path
		}
	}
	if idx := strings.Index(raw, "?"); idx >= 0 {
		raw = raw[:idx]
	}
	if !strings.HasPrefix(raw, "/") {
		return ""
	}
	return raw
}

func normalizeHTTPPath(raw string, stripPrefixes bool) string {
	raw = routePathOnly(raw)
	if raw == "" {
		return ""
	}
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = regexp.MustCompile(`\$\{[^}]+\}`).ReplaceAllString(raw, "{}")
	raw = regexp.MustCompile(`\{[^}/]+\}`).ReplaceAllString(raw, "{}")
	raw = regexp.MustCompile(`:[a-z_][a-z0-9_]*`).ReplaceAllString(raw, "{}")
	raw = regexp.MustCompile(`/+`).ReplaceAllString(raw, "/")
	raw = strings.TrimRight(raw, "/")
	if raw == "" {
		raw = "/"
	}
	if stripPrefixes {
		raw = stripHTTPRoutePrefixes(raw)
	}
	return raw
}

func stripHTTPRoutePrefixes(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for len(parts) > 0 {
		switch parts[0] {
		case "api", "v1", "v2", "public", "internal":
			parts = parts[1:]
		default:
			if len(parts) == 0 {
				return "/"
			}
			return "/" + strings.Join(parts, "/")
		}
	}
	return "/"
}

func isQueuePublish(depType string) bool {
	depType = strings.ToLower(strings.TrimSpace(depType))
	return depType == "queue_publish" || depType == "stream_publish" || strings.Contains(depType, "publish")
}

func normalizeQueueTopic(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(strings.Trim(raw, `"'`)))
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "queue:") {
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "queue:"))
	}
	if strings.HasPrefix(raw, "arn:") {
		if idx := strings.LastIndex(raw, ":"); idx >= 0 && idx+1 < len(raw) {
			raw = raw[idx+1:]
		}
	}
	if strings.Contains(raw, "://") {
		if u, err := url.Parse(raw); err == nil {
			raw = strings.Trim(strings.TrimSpace(u.Path), "/")
			if idx := strings.LastIndex(raw, "/"); idx >= 0 && idx+1 < len(raw) {
				raw = raw[idx+1:]
			}
		}
	}
	fifo := strings.HasSuffix(raw, ".fifo")
	raw = strings.TrimSuffix(raw, ".fifo")
	raw = queueSepRe.ReplaceAllString(raw, "")
	if fifo {
		raw += ".fifo"
	}
	return raw
}

func normalizeIdentity(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(strings.Trim(raw, `"'`)))
	if raw == "" {
		return ""
	}
	if host := normalizeHostname(raw); host != "" {
		raw = host
	}
	raw = strings.TrimSuffix(raw, ".svc.cluster.local")
	raw = strings.TrimSuffix(raw, ".cluster.local")
	raw = strings.TrimSuffix(raw, ".local")
	raw = strings.TrimSuffix(raw, ".internal")
	return strings.Trim(raw, "/")
}

func normalizeHostname(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(strings.Trim(raw, `"'`)))
	if raw == "" {
		return ""
	}
	raw = strings.TrimPrefix(raw, "lb://")
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		raw = u.Host
	} else if idx := strings.Index(raw, "/"); idx >= 0 {
		raw = raw[:idx]
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	} else if idx := strings.LastIndex(raw, ":"); idx >= 0 && idx+1 < len(raw) && allDigits(raw[idx+1:]) {
		raw = raw[:idx]
	}
	raw = strings.TrimSuffix(raw, ".svc.cluster.local")
	raw = strings.TrimSuffix(raw, ".cluster.local")
	raw = strings.TrimSuffix(raw, ".local")
	return strings.Trim(raw, ".")
}

func tokenBoundaryContains(haystack, needle string) bool {
	haystack = strings.ToLower(strings.TrimSpace(haystack))
	needle = strings.ToLower(strings.TrimSpace(needle))
	if len(needle) < 5 || haystack == "" {
		return false
	}
	re := regexp.MustCompile(`(^|[^a-z0-9])` + regexp.QuoteMeta(needle) + `([^a-z0-9]|$)`)
	return re.MatchString(haystack)
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var queueSepRe = regexp.MustCompile(`[-_.]+`)
