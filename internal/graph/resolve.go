package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"diffmind/internal/bundleio"
	"diffmind/internal/facts"
	"diffmind/internal/graphschema"
)

type nodeRef struct {
	id    string
	label string
	typ   string
}

func (g *graphBuilder) addServiceNodes() {
	for _, svc := range g.services {
		id := serviceNodeID(svc.spec.ID)
		g.addNode(graphschema.Node{
			ID:         id,
			Type:       "service",
			Label:      svc.spec.Name,
			ServiceID:  svc.spec.ID,
			Attributes: map[string]any{"service_id": svc.spec.ID, "name": svc.spec.Name, "repo_path": svc.spec.RepoPath},
			Confidence: 1.0,
			Inferred:   false,
		})
	}
}

func (g *graphBuilder) addEndpointNodes() {
	for i := range g.services {
		svc := &g.services[i]
		serviceNode := serviceNodeID(svc.spec.ID)
		for _, e := range svc.bundle.Entities {
			if e.Type != "Endpoint" {
				continue
			}
			nodeID := endpointNodeID(svc.spec.ID, e.ID)
			path := fmt.Sprint(e.Attributes["path"])
			method := fmt.Sprint(e.Attributes["method"])
			g.addNode(graphschema.Node{
				ID:         nodeID,
				Type:       "endpoint",
				Label:      strings.TrimSpace(method + " " + path),
				ServiceID:  svc.spec.ID,
				Attributes: cloneMap(e.Attributes),
				Confidence: e.Confidence,
				Inferred:   false,
			})
			endpointNode := g.nodeByID[nodeID]
			endpointNode.Attributes["path_normalized"] = normalizePath(path)
			g.nodeByID[nodeID] = endpointNode
			svc.endpointNodes[e.ID] = nodeID
			g.addEdge(graphschema.Edge{
				ID:           edgeID("service_exposes_endpoint", serviceNode, nodeID, e.ID),
				Type:         "service_exposes_endpoint",
				SourceID:     serviceNode,
				TargetID:     nodeID,
				Attributes:   map[string]any{"source": "bundle"},
				Confidence:   e.Confidence,
				Inferred:     false,
				EvidenceRefs: buildEvidenceRefs(svc.analyzer, e.FactIDs, e.EvidenceIDs),
			})
		}
	}
}

func (g *graphBuilder) resolveAPIEdges() {
	for i := range g.services {
		sourceService := &g.services[i]
		for _, e := range sourceService.bundle.Entities {
			if e.Type != "ExternalCall" {
				continue
			}
			protocol := strings.ToLower(strings.TrimSpace(fmt.Sprint(e.Attributes["protocol"])))
			if protocol != "" && protocol != "http" {
				continue
			}

			srcServiceNode := serviceNodeID(sourceService.spec.ID)
			rawTarget := strings.TrimSpace(fmt.Sprint(e.Attributes["target"]))
			target := g.resolveExternalCallTarget(sourceService, rawTarget)
			targetServiceHint := strings.TrimSpace(fmt.Sprint(e.Attributes["target_service"]))
			baseURLRefHint := strings.TrimSpace(fmt.Sprint(e.Attributes["base_url_ref"]))
			method := strings.ToUpper(strings.TrimSpace(fmt.Sprint(e.Attributes["method"])))
			if method == "" {
				method = "UNKNOWN"
			}

			var targetSvc *serviceInput
			ambiguousCandidates := []string{}
			inferred := false
			serviceMatch := ""
			targetSvc, inferred, serviceMatch = g.matchTargetServiceFromHints(sourceService, targetServiceHint, baseURLRefHint)
			if targetSvc == nil {
				var candidates []string
				targetSvc, candidates = g.matchTargetServiceDetailed(target, sourceService.spec.ID)
				if len(candidates) > 1 {
					ambiguousCandidates = candidates
				}
				if targetSvc != nil {
					serviceMatch = "target_url"
				}
			}
			if targetSvc == nil && rawTarget != target {
				var candidates []string
				targetSvc, candidates = g.matchTargetServiceDetailed(rawTarget, sourceService.spec.ID)
				if len(candidates) > 1 {
					ambiguousCandidates = candidates
				}
				if targetSvc != nil {
					serviceMatch = "target_raw"
				}
			}
			if targetSvc == nil {
				var candidates []string
				targetSvc, candidates = g.inferTargetServiceDetailed(target, sourceService.spec.ID)
				if len(candidates) > 1 {
					ambiguousCandidates = candidates
				}
				inferred = true
				if targetSvc != nil {
					serviceMatch = "target_alias_inferred"
				}
			}
			if targetSvc == nil {
				if len(ambiguousCandidates) > 1 {
					g.addUnresolvedAPICall(sourceService, e, method, rawTarget, target, targetServiceHint, baseURLRefHint, ambiguousCandidates)
					continue
				}
				continue
			}

			evidence := buildEvidenceRefs(sourceService.analyzer, e.FactIDs, e.EvidenceIDs)
			serviceToServiceID := edgeID("service_calls_service", srcServiceNode, serviceNodeID(targetSvc.spec.ID), method+"|"+target)
			attrs := map[string]any{
				"method":            method,
				"target":            target,
				"library":           e.Attributes["library"],
				"source_service_id": sourceService.spec.ID,
				"source_repo_path":  sourceService.spec.RepoPath,
				"target_service_id": targetSvc.spec.ID,
				"target_repo_path":  targetSvc.spec.RepoPath,
			}
			if target != rawTarget {
				attrs["target_raw"] = rawTarget
				attrs["resolved_from_config"] = true
			}
			if targetServiceHint != "" {
				attrs["target_service_hint"] = targetServiceHint
			}
			if baseURLRefHint != "" {
				attrs["base_url_ref"] = baseURLRefHint
			}
			if serviceMatch != "" {
				attrs["service_match"] = serviceMatch
			}
			if len(ambiguousCandidates) > 1 {
				attrs["service_match_ambiguous"] = true
				attrs["service_match_candidates"] = ambiguousCandidates
			}
			g.addEdge(graphschema.Edge{
				ID:           serviceToServiceID,
				Type:         "service_calls_service",
				SourceID:     srcServiceNode,
				TargetID:     serviceNodeID(targetSvc.spec.ID),
				Attributes:   attrs,
				Confidence:   edgeConfidence(e.Confidence, inferred),
				Inferred:     inferred,
				EvidenceRefs: evidence,
			})

			endpointID := g.matchTargetEndpoint(*targetSvc, method, target)
			if endpointID == "" {
				continue
			}
			serviceToEndpointID := edgeID("service_calls_endpoint", srcServiceNode, endpointID, method+"|"+target)
			epAttrs := map[string]any{
				"method":                 method,
				"target":                 target,
				"target_path_normalized": normalizePath(target),
				"source_service_id":      sourceService.spec.ID,
				"source_repo_path":       sourceService.spec.RepoPath,
				"target_service_id":      targetSvc.spec.ID,
				"target_repo_path":       targetSvc.spec.RepoPath,
			}
			if target != rawTarget {
				epAttrs["target_raw"] = rawTarget
				epAttrs["resolved_from_config"] = true
			}
			if targetServiceHint != "" {
				epAttrs["target_service_hint"] = targetServiceHint
			}
			if baseURLRefHint != "" {
				epAttrs["base_url_ref"] = baseURLRefHint
			}
			if serviceMatch != "" {
				epAttrs["service_match"] = serviceMatch
			}
			if len(ambiguousCandidates) > 1 {
				epAttrs["service_match_ambiguous"] = true
				epAttrs["service_match_candidates"] = ambiguousCandidates
			}
			g.addEdge(graphschema.Edge{
				ID:           serviceToEndpointID,
				Type:         "service_calls_endpoint",
				SourceID:     srcServiceNode,
				TargetID:     endpointID,
				Attributes:   epAttrs,
				Confidence:   edgeConfidence(e.Confidence, inferred),
				Inferred:     inferred,
				EvidenceRefs: evidence,
			})
		}
	}
}

func (g *graphBuilder) addUnresolvedAPICall(
	sourceService *serviceInput,
	entity bundleio.Entity,
	method string,
	rawTarget string,
	resolvedTarget string,
	targetServiceHint string,
	baseURLRefHint string,
	candidates []string,
) {
	if sourceService == nil {
		return
	}
	serviceNode := serviceNodeID(sourceService.spec.ID)
	entityID := strings.TrimSpace(entity.ID)
	if entityID == "" {
		entityID = stableID(method + "|" + rawTarget + "|" + resolvedTarget + "|" + targetServiceHint + "|" + baseURLRefHint)
	}
	nodeID := unresolvedAPICallNodeID(sourceService.spec.ID, entityID)

	attrs := map[string]any{
		"protocol":                 "http",
		"method":                   method,
		"target":                   resolvedTarget,
		"status":                   "needs_review",
		"reason":                   "ambiguous_service_match",
		"service_match_candidates": uniqueStrings(candidates),
		"source_service_id":        sourceService.spec.ID,
		"source_repo_path":         sourceService.spec.RepoPath,
	}
	if strings.TrimSpace(rawTarget) != "" && rawTarget != resolvedTarget {
		attrs["target_raw"] = rawTarget
		attrs["resolved_from_config"] = true
	}
	if strings.TrimSpace(targetServiceHint) != "" {
		attrs["target_service_hint"] = targetServiceHint
	}
	if strings.TrimSpace(baseURLRefHint) != "" {
		attrs["base_url_ref"] = baseURLRefHint
	}

	g.addNode(graphschema.Node{
		ID:         nodeID,
		Type:       "unresolved_api_call",
		Label:      "unresolved API call: " + method,
		ServiceID:  sourceService.spec.ID,
		Attributes: attrs,
		Confidence: edgeConfidence(entity.Confidence, true),
		Inferred:   true,
	})
	evidence := buildEvidenceRefs(sourceService.analyzer, entity.FactIDs, entity.EvidenceIDs)
	g.addEdge(graphschema.Edge{
		ID:           edgeID("service_has_unresolved_api_call", serviceNode, nodeID, entityID),
		Type:         "service_has_unresolved_api_call",
		SourceID:     serviceNode,
		TargetID:     nodeID,
		Attributes:   map[string]any{"status": "needs_review", "reason": "ambiguous_service_match"},
		Confidence:   edgeConfidence(entity.Confidence, true),
		Inferred:     true,
		EvidenceRefs: evidence,
	})
}

func (g *graphBuilder) matchTargetServiceFromHints(sourceService *serviceInput, targetServiceHint string, baseURLRefHint string) (*serviceInput, bool, string) {
	if sourceService == nil {
		return nil, false, ""
	}
	if svc, inferred := g.matchServiceByHint(targetServiceHint, sourceService.spec.ID); svc != nil {
		if inferred {
			return svc, true, "target_service_inferred"
		}
		return svc, false, "target_service"
	}
	baseURL := g.resolveServiceHintValue(sourceService, baseURLRefHint)
	if baseURL == "" {
		return nil, false, ""
	}
	if svc := g.matchTargetService(baseURL, sourceService.spec.ID); svc != nil {
		return svc, false, "base_url_ref"
	}
	if svc := g.inferTargetService(baseURL, sourceService.spec.ID); svc != nil {
		return svc, true, "base_url_ref_inferred"
	}
	return nil, false, ""
}

func (g *graphBuilder) matchServiceByHint(rawHint string, sourceServiceID string) (*serviceInput, bool) {
	hint := normalizeTarget(rawHint)
	if hint == "" {
		return nil, false
	}
	sourceEnv := g.serviceEnvTags(sourceServiceID)
	cands := []string{hint}
	for _, c := range canonicalServiceHintCandidates(hint) {
		cands = append(cands, c)
	}
	cands = uniqueStrings(cands)
	// Exact alias/canonical-key match first.
	for i := range g.services {
		svc := &g.services[i]
		if svc.spec.ID == sourceServiceID {
			continue
		}
		if !environmentsCompatible(sourceEnv, svc.envTags) {
			continue
		}
		aliases := append(serviceAliases(svc.spec), canonicalServiceKeys(svc.spec)...)
		for _, alias := range aliases {
			alias = normalizeTarget(alias)
			for _, cand := range cands {
				if cand != "" && cand == alias {
					return svc, false
				}
			}
		}
	}
	// Fallback fuzzy alias contains match.
	for i := range g.services {
		svc := &g.services[i]
		if svc.spec.ID == sourceServiceID {
			continue
		}
		if !environmentsCompatible(sourceEnv, svc.envTags) {
			continue
		}
		aliases := append(serviceAliases(svc.spec), canonicalServiceKeys(svc.spec)...)
		for _, alias := range aliases {
			alias = normalizeTarget(alias)
			if len(alias) < 4 {
				continue
			}
			for _, cand := range cands {
				if len(cand) < 4 {
					continue
				}
				if strings.Contains(cand, alias) || strings.Contains(alias, cand) {
					return svc, true
				}
			}
		}
	}
	return nil, false
}

func canonicalServiceHintCandidates(v string) []string {
	v = normalizeTarget(v)
	if v == "" {
		return nil
	}
	parts := strings.FieldsFunc(v, func(r rune) bool {
		return r == '.' || r == '/' || r == ':' || r == '@'
	})
	out := []string{v}
	if len(parts) > 0 {
		out = append(out, parts[len(parts)-1])
	}
	trimmed := strings.TrimPrefix(v, "service-")
	trimmed = strings.TrimSuffix(trimmed, "-service")
	trimmed = strings.TrimSuffix(trimmed, ".service")
	if trimmed != "" {
		out = append(out, trimmed)
	}
	return uniqueStrings(out)
}

func (g *graphBuilder) resolveServiceHintValue(svc *serviceInput, rawHint string) string {
	rawHint = strings.TrimSpace(rawHint)
	if rawHint == "" {
		return ""
	}
	if strings.HasPrefix(rawHint, "http://") || strings.HasPrefix(rawHint, "https://") {
		return rawHint
	}
	cfgKey := configKeyFromRef(rawHint)
	if cfgKey == "" {
		return ""
	}
	return preferredConfigResolvedValue(svc.bundle.Entities, cfgKey)
}

func configKeyFromRef(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(raw), "cfg:") {
		return strings.TrimSpace(strings.TrimPrefix(raw, "cfg:"))
	}
	if strings.HasPrefix(raw, "${") && strings.HasSuffix(raw, "}") && len(raw) > 3 {
		inner := strings.TrimSpace(raw[2 : len(raw)-1])
		if idx := strings.Index(inner, ":"); idx >= 0 {
			inner = strings.TrimSpace(inner[:idx])
		}
		return inner
	}
	if !strings.Contains(raw, "://") && !strings.Contains(raw, "/") {
		if strings.Contains(raw, ".") || strings.Contains(raw, "_") || strings.Contains(raw, "-") {
			return raw
		}
	}
	return ""
}

func (g *graphBuilder) resolveCodeQueueAndDBEdges() {
	queueNodes := map[string]string{}
	dbNodes := map[string]string{}

	for i := range g.services {
		svc := &g.services[i]
		src := serviceNodeID(svc.spec.ID)
		for _, e := range svc.bundle.Entities {
			if e.Type != "ExternalCall" {
				continue
			}
			protocol := strings.ToLower(strings.TrimSpace(fmt.Sprint(e.Attributes["protocol"])))
			rawTarget := strings.TrimSpace(fmt.Sprint(e.Attributes["target"]))
			target := g.resolveExternalCallTarget(svc, rawTarget)
			method := strings.ToUpper(strings.TrimSpace(fmt.Sprint(e.Attributes["method"])))
			if target == "" {
				continue
			}
			evidence := buildEvidenceRefs(svc.analyzer, e.FactIDs, e.EvidenceIDs)
			switch protocol {
			case "queue":
				canonical := canonicalQueueKey(target)
				queueID, ok := queueNodes[target]
				if !ok {
					queueID = queueNodeID(target)
					queueNodes[target] = queueID
					g.addNode(graphschema.Node{
						ID:         queueID,
						Type:       "queue",
						Label:      target,
						Attributes: map[string]any{"name": target, "source": "analyzer", "canonical_key": canonical},
						Confidence: e.Confidence,
						Inferred:   false,
					})
				}
				if method == "CONSUME" {
					attrs := map[string]any{
						"topic":         target,
						"source":        "analyzer",
						"library":       e.Attributes["library"],
						"canonical_key": canonical,
					}
					if target != rawTarget {
						attrs["topic_raw"] = rawTarget
						attrs["resolved_from_config"] = true
					}
					g.addEdge(graphschema.Edge{
						ID:           edgeID("queue_delivers_to_service", queueID, src, target+"|"+method),
						Type:         "queue_delivers_to_service",
						SourceID:     queueID,
						TargetID:     src,
						Attributes:   attrs,
						Confidence:   e.Confidence,
						Inferred:     false,
						EvidenceRefs: evidence,
					})
				} else {
					attrs := map[string]any{
						"topic":         target,
						"source":        "analyzer",
						"library":       e.Attributes["library"],
						"canonical_key": canonical,
					}
					if target != rawTarget {
						attrs["topic_raw"] = rawTarget
						attrs["resolved_from_config"] = true
					}
					g.addEdge(graphschema.Edge{
						ID:           edgeID("service_publishes_queue", src, queueID, target+"|"+method),
						Type:         "service_publishes_queue",
						SourceID:     src,
						TargetID:     queueID,
						Attributes:   attrs,
						Confidence:   e.Confidence,
						Inferred:     false,
						EvidenceRefs: evidence,
					})
				}
			case "db":
				canonical := canonicalDatabaseKey(target)
				dbID, ok := dbNodes[target]
				if !ok {
					dbID = databaseNodeID(target)
					dbNodes[target] = dbID
					g.addNode(graphschema.Node{
						ID:         dbID,
						Type:       "database",
						Label:      target,
						Attributes: map[string]any{"name": target, "source": "analyzer", "canonical_key": canonical},
						Confidence: e.Confidence,
						Inferred:   false,
					})
				}
				if method == "READ" {
					attrs := map[string]any{
						"database":      target,
						"source":        "analyzer",
						"library":       e.Attributes["library"],
						"canonical_key": canonical,
					}
					if target != rawTarget {
						attrs["database_raw"] = rawTarget
						attrs["resolved_from_config"] = true
					}
					g.addEdge(graphschema.Edge{
						ID:           edgeID("service_reads_db", src, dbID, target+"|"+method),
						Type:         "service_reads_db",
						SourceID:     src,
						TargetID:     dbID,
						Attributes:   attrs,
						Confidence:   e.Confidence,
						Inferred:     false,
						EvidenceRefs: evidence,
					})
				} else {
					attrs := map[string]any{
						"database":      target,
						"source":        "analyzer",
						"library":       e.Attributes["library"],
						"canonical_key": canonical,
					}
					if target != rawTarget {
						attrs["database_raw"] = rawTarget
						attrs["resolved_from_config"] = true
					}
					g.addEdge(graphschema.Edge{
						ID:           edgeID("service_writes_db", src, dbID, target+"|"+method),
						Type:         "service_writes_db",
						SourceID:     src,
						TargetID:     dbID,
						Attributes:   attrs,
						Confidence:   e.Confidence,
						Inferred:     false,
						EvidenceRefs: evidence,
					})
				}
			}
		}
	}
}

func (g *graphBuilder) resolveManifestQueueAndDBEdges() {
	queueNodes := map[string]string{}
	dbNodes := map[string]string{}

	for _, svc := range g.services {
		src := serviceNodeID(svc.spec.ID)
		for _, topic := range uniqueStrings(svc.spec.QueuePublishes) {
			canonical := canonicalQueueKey(topic)
			qNodeID := queueNodeID(topic)
			queueNodes[topic] = qNodeID
			g.addNode(graphschema.Node{
				ID:         qNodeID,
				Type:       "queue",
				Label:      topic,
				Attributes: map[string]any{"name": topic, "canonical_key": canonical},
				Confidence: 1.0,
				Inferred:   false,
			})
			g.addEdge(graphschema.Edge{
				ID:         edgeID("service_publishes_queue", src, qNodeID, topic),
				Type:       "service_publishes_queue",
				SourceID:   src,
				TargetID:   qNodeID,
				Attributes: map[string]any{"topic": topic, "source": "manifest", "canonical_key": canonical},
				Confidence: 1.0,
				Inferred:   false,
			})
		}
		for _, topic := range uniqueStrings(svc.spec.QueueConsumes) {
			canonical := canonicalQueueKey(topic)
			qNodeID, ok := queueNodes[topic]
			if !ok {
				qNodeID = queueNodeID(topic)
				queueNodes[topic] = qNodeID
				g.addNode(graphschema.Node{
					ID:         qNodeID,
					Type:       "queue",
					Label:      topic,
					Attributes: map[string]any{"name": topic, "canonical_key": canonical},
					Confidence: 1.0,
					Inferred:   false,
				})
			}
			g.addEdge(graphschema.Edge{
				ID:         edgeID("queue_delivers_to_service", qNodeID, src, topic),
				Type:       "queue_delivers_to_service",
				SourceID:   qNodeID,
				TargetID:   src,
				Attributes: map[string]any{"topic": topic, "source": "manifest", "canonical_key": canonical},
				Confidence: 1.0,
				Inferred:   false,
			})
		}

		for _, db := range uniqueStrings(svc.spec.DBReads) {
			canonical := canonicalDatabaseKey(db)
			dbNodeID, ok := dbNodes[db]
			if !ok {
				dbNodeID = databaseNodeID(db)
				dbNodes[db] = dbNodeID
				g.addNode(graphschema.Node{
					ID:         dbNodeID,
					Type:       "database",
					Label:      db,
					Attributes: map[string]any{"name": db, "canonical_key": canonical},
					Confidence: 1.0,
					Inferred:   false,
				})
			}
			g.addEdge(graphschema.Edge{
				ID:         edgeID("service_reads_db", src, dbNodeID, db),
				Type:       "service_reads_db",
				SourceID:   src,
				TargetID:   dbNodeID,
				Attributes: map[string]any{"database": db, "source": "manifest", "canonical_key": canonical},
				Confidence: 1.0,
				Inferred:   false,
			})
		}
		for _, db := range uniqueStrings(svc.spec.DBWrites) {
			canonical := canonicalDatabaseKey(db)
			dbNodeID, ok := dbNodes[db]
			if !ok {
				dbNodeID = databaseNodeID(db)
				dbNodes[db] = dbNodeID
				g.addNode(graphschema.Node{
					ID:         dbNodeID,
					Type:       "database",
					Label:      db,
					Attributes: map[string]any{"name": db, "canonical_key": canonical},
					Confidence: 1.0,
					Inferred:   false,
				})
			}
			g.addEdge(graphschema.Edge{
				ID:         edgeID("service_writes_db", src, dbNodeID, db),
				Type:       "service_writes_db",
				SourceID:   src,
				TargetID:   dbNodeID,
				Attributes: map[string]any{"database": db, "source": "manifest", "canonical_key": canonical},
				Confidence: 1.0,
				Inferred:   false,
			})
		}
	}
}

func (g *graphBuilder) resolveRuntimeBuildDeployEdges() {
	for i := range g.services {
		svc := &g.services[i]
		serviceNode := serviceNodeID(svc.spec.ID)

		runtimeNodeByEntity := map[string]string{}
		pipelineNodeByEntity := map[string]string{}
		artifactNodeByEntity := map[string]string{}
		deployNodeByEntity := map[string]string{}

		for _, e := range svc.bundle.Entities {
			evidence := buildEvidenceRefs(svc.analyzer, e.FactIDs, e.EvidenceIDs)
			switch e.Type {
			case "RuntimeUnit":
				nID := runtimeNodeID(svc.spec.ID, e.ID)
				runtimeNodeByEntity[e.ID] = nID
				label := strings.TrimSpace(fmt.Sprintf("%v %v", e.Attributes["language"], e.Attributes["kind"]))
				if label == "" {
					label = e.ID
				}
				g.addNode(graphschema.Node{
					ID:         nID,
					Type:       "runtime_unit",
					Label:      label,
					ServiceID:  svc.spec.ID,
					Attributes: cloneMap(e.Attributes),
					Confidence: e.Confidence,
					Inferred:   false,
				})
				g.addEdge(graphschema.Edge{
					ID:           edgeID("service_has_runtime_unit", serviceNode, nID, e.ID),
					Type:         "service_has_runtime_unit",
					SourceID:     serviceNode,
					TargetID:     nID,
					Attributes:   map[string]any{"source": "bundle"},
					Confidence:   e.Confidence,
					Inferred:     false,
					EvidenceRefs: evidence,
				})
			case "PipelineStep":
				nID := pipelineNodeID(svc.spec.ID, e.ID)
				pipelineNodeByEntity[e.ID] = nID
				label := strings.TrimSpace(fmt.Sprint(e.Attributes["kind"]))
				if v := strings.TrimSpace(fmt.Sprint(e.Attributes["value"])); v != "" {
					label = strings.TrimSpace(label + " " + v)
				}
				if label == "" {
					label = e.ID
				}
				g.addNode(graphschema.Node{
					ID:         nID,
					Type:       "pipeline_step",
					Label:      label,
					ServiceID:  svc.spec.ID,
					Attributes: cloneMap(e.Attributes),
					Confidence: e.Confidence,
					Inferred:   false,
				})
				g.addEdge(graphschema.Edge{
					ID:           edgeID("service_built_by_pipeline_step", serviceNode, nID, e.ID),
					Type:         "service_built_by_pipeline_step",
					SourceID:     serviceNode,
					TargetID:     nID,
					Attributes:   map[string]any{"source": "bundle"},
					Confidence:   e.Confidence,
					Inferred:     false,
					EvidenceRefs: evidence,
				})
			case "BuildArtifact":
				nID := buildArtifactNodeID(svc.spec.ID, e.ID)
				artifactNodeByEntity[e.ID] = nID
				label := strings.TrimSpace(fmt.Sprint(e.Attributes["name"]))
				if label == "" {
					label = strings.TrimSpace(fmt.Sprint(e.Attributes["artifact_type"]))
				}
				if label == "" {
					label = e.ID
				}
				g.addNode(graphschema.Node{
					ID:         nID,
					Type:       "build_artifact",
					Label:      label,
					ServiceID:  svc.spec.ID,
					Attributes: cloneMap(e.Attributes),
					Confidence: e.Confidence,
					Inferred:   false,
				})
			case "Deployment":
				nID := deploymentNodeID(svc.spec.ID, e.ID)
				deployNodeByEntity[e.ID] = nID
				label := strings.TrimSpace(fmt.Sprint(e.Attributes["name"]))
				if label == "" {
					label = strings.TrimSpace(fmt.Sprint(e.Attributes["platform"]))
				}
				if label == "" {
					label = e.ID
				}
				g.addNode(graphschema.Node{
					ID:         nID,
					Type:       "deployment",
					Label:      label,
					ServiceID:  svc.spec.ID,
					Attributes: cloneMap(e.Attributes),
					Confidence: e.Confidence,
					Inferred:   false,
				})
				g.addEdge(graphschema.Edge{
					ID:           edgeID("service_deployed_to_runtime", serviceNode, nID, e.ID),
					Type:         "service_deployed_to_runtime",
					SourceID:     serviceNode,
					TargetID:     nID,
					Attributes:   map[string]any{"source": "bundle"},
					Confidence:   e.Confidence,
					Inferred:     false,
					EvidenceRefs: evidence,
				})
			case "InfraResource":
				nID := infraResourceNodeID(svc.spec.ID, e.ID)
				label := strings.TrimSpace(fmt.Sprint(e.Attributes["name"]))
				if label == "" {
					label = strings.TrimSpace(fmt.Sprint(e.Attributes["kind"]))
				}
				if label == "" {
					label = e.ID
				}
				g.addNode(graphschema.Node{
					ID:         nID,
					Type:       "infra_resource",
					Label:      label,
					ServiceID:  svc.spec.ID,
					Attributes: cloneMap(e.Attributes),
					Confidence: e.Confidence,
					Inferred:   false,
				})
				g.addEdge(graphschema.Edge{
					ID:           edgeID("service_uses_infra_resource", serviceNode, nID, e.ID),
					Type:         "service_uses_infra_resource",
					SourceID:     serviceNode,
					TargetID:     nID,
					Attributes:   map[string]any{"source": "bundle"},
					Confidence:   e.Confidence,
					Inferred:     false,
					EvidenceRefs: evidence,
				})
			}
		}

		// Link pipeline steps to build artifacts in the same service.
		pipelineNodes := mapValues(pipelineNodeByEntity)
		artifactNodes := mapValues(artifactNodeByEntity)
		for _, p := range pipelineNodes {
			for _, a := range artifactNodes {
				g.addEdge(graphschema.Edge{
					ID:         edgeID("pipeline_step_produces_artifact", p, a, svc.spec.ID),
					Type:       "pipeline_step_produces_artifact",
					SourceID:   p,
					TargetID:   a,
					Attributes: map[string]any{"source": "inferred-service-link"},
					Confidence: 0.7,
					Inferred:   true,
				})
			}
		}
		// Link artifacts to deployments in the same service.
		deployNodes := mapValues(deployNodeByEntity)
		for _, a := range artifactNodes {
			for _, d := range deployNodes {
				g.addEdge(graphschema.Edge{
					ID:         edgeID("artifact_deployed_to_runtime", a, d, svc.spec.ID),
					Type:       "artifact_deployed_to_runtime",
					SourceID:   a,
					TargetID:   d,
					Attributes: map[string]any{"source": "inferred-service-link"},
					Confidence: 0.7,
					Inferred:   true,
				})
			}
		}
	}
}

func (g *graphBuilder) resolveConfigAndSensitiveEdges() {
	for i := range g.services {
		svc := &g.services[i]
		serviceNode := serviceNodeID(svc.spec.ID)

		runtimeEntityToNode := map[string]string{}
		for _, e := range svc.bundle.Entities {
			if e.Type == "RuntimeUnit" {
				runtimeEntityToNode[e.ID] = runtimeNodeID(svc.spec.ID, e.ID)
			}
		}
		configByKeyEnv := map[string]string{}

		for _, e := range svc.bundle.Entities {
			evidence := buildEvidenceRefs(svc.analyzer, e.FactIDs, e.EvidenceIDs)
			switch e.Type {
			case "ConfigKey":
				key := strings.TrimSpace(fmt.Sprint(e.Attributes["key"]))
				if key == "" {
					continue
				}
				env := strings.TrimSpace(fmt.Sprint(e.Attributes["environment"]))
				if env == "" {
					env = "default"
				}
				configKey := strings.ToLower(key + "|" + env)
				cfgNodeID, exists := configByKeyEnv[configKey]
				if !exists {
					cfgNodeID = configNodeID(svc.spec.ID, key, env)
					configByKeyEnv[configKey] = cfgNodeID
					label := key
					if env != "default" {
						label = key + " (" + env + ")"
					}
					g.addNode(graphschema.Node{
						ID:        cfgNodeID,
						Type:      "config_key",
						Label:     label,
						ServiceID: svc.spec.ID,
						Attributes: map[string]any{
							"key":         key,
							"environment": env,
							"source_kind": e.Attributes["source_kind"],
							"sensitive":   e.Attributes["sensitive"],
							"file":        e.Attributes["file"],
							"pattern":     e.Attributes["pattern"],
						},
						Confidence: e.Confidence,
						Inferred:   false,
					})
				}

				g.addEdge(graphschema.Edge{
					ID:           edgeID("service_uses_config", serviceNode, cfgNodeID, key+"|"+env),
					Type:         "service_uses_config",
					SourceID:     serviceNode,
					TargetID:     cfgNodeID,
					Attributes:   map[string]any{"key": key, "environment": env},
					Confidence:   e.Confidence,
					Inferred:     false,
					EvidenceRefs: evidence,
				})
				if runtimeID := strings.TrimSpace(fmt.Sprint(e.Attributes["runtime_unit_id"])); runtimeID != "" {
					if rtNode, ok := runtimeEntityToNode[runtimeID]; ok {
						g.addEdge(graphschema.Edge{
							ID:           edgeID("runtime_unit_reads_config", rtNode, cfgNodeID, key+"|"+env),
							Type:         "runtime_unit_reads_config",
							SourceID:     rtNode,
							TargetID:     cfgNodeID,
							Attributes:   map[string]any{"key": key, "environment": env},
							Confidence:   e.Confidence,
							Inferred:     false,
							EvidenceRefs: evidence,
						})
					}
				}
				envNode := environmentNodeID(env)
				g.addNode(graphschema.Node{
					ID:         envNode,
					Type:       "environment",
					Label:      env,
					Attributes: map[string]any{"name": env},
					Confidence: 1.0,
					Inferred:   false,
				})
				g.addEdge(graphschema.Edge{
					ID:         edgeID("config_scoped_to_environment", cfgNodeID, envNode, key+"|"+env),
					Type:       "config_scoped_to_environment",
					SourceID:   cfgNodeID,
					TargetID:   envNode,
					Attributes: map[string]any{"environment": env},
					Confidence: e.Confidence,
					Inferred:   false,
				})
			case "SensitiveSurface":
				sNodeID := sensitiveSurfaceNodeID(svc.spec.ID, e.ID)
				label := strings.TrimSpace(fmt.Sprint(e.Attributes["key"]))
				if label == "" {
					label = strings.TrimSpace(fmt.Sprint(e.Attributes["reference"]))
				}
				if label == "" {
					label = "sensitive_surface"
				}
				g.addNode(graphschema.Node{
					ID:        sNodeID,
					Type:      "sensitive_surface",
					Label:     label,
					ServiceID: svc.spec.ID,
					Attributes: map[string]any{
						"kind":           e.Attributes["kind"],
						"classification": e.Attributes["classification"],
						"environment":    e.Attributes["environment"],
						"source_kind":    e.Attributes["source_kind"],
						"key":            e.Attributes["key"],
						"reference":      e.Attributes["reference"],
					},
					Confidence: e.Confidence,
					Inferred:   false,
				})
				g.addEdge(graphschema.Edge{
					ID:           edgeID("service_exposes_sensitive_surface", serviceNode, sNodeID, e.ID),
					Type:         "service_exposes_sensitive_surface",
					SourceID:     serviceNode,
					TargetID:     sNodeID,
					Attributes:   map[string]any{"kind": e.Attributes["kind"]},
					Confidence:   e.Confidence,
					Inferred:     false,
					EvidenceRefs: evidence,
				})

				key := strings.TrimSpace(fmt.Sprint(e.Attributes["key"]))
				env := strings.TrimSpace(fmt.Sprint(e.Attributes["environment"]))
				if env == "" {
					env = "default"
				}
				if key != "" {
					if cfgNodeID, ok := configByKeyEnv[strings.ToLower(key+"|"+env)]; ok {
						g.addEdge(graphschema.Edge{
							ID:         edgeID("config_has_sensitive_surface", cfgNodeID, sNodeID, key+"|"+env),
							Type:       "config_has_sensitive_surface",
							SourceID:   cfgNodeID,
							TargetID:   sNodeID,
							Attributes: map[string]any{"classification": e.Attributes["classification"]},
							Confidence: e.Confidence,
							Inferred:   false,
						})
					}
				}
			}
		}
	}
}

func (g *graphBuilder) resolveDependencyOwnershipEdges() {
	for i := range g.services {
		svc := &g.services[i]
		serviceNode := serviceNodeID(svc.spec.ID)
		ownershipRules := make([]bundleioEntityRef, 0, 8)
		depNodesByName := map[string]string{}

		for _, e := range svc.bundle.Entities {
			if e.Type == "OwnershipRule" {
				ownershipRules = append(ownershipRules, bundleioEntityRef{
					id:          e.ID,
					attributes:  e.Attributes,
					confidence:  e.Confidence,
					factIDs:     e.FactIDs,
					evidenceIDs: e.EvidenceIDs,
				})
			}
		}

		for _, e := range svc.bundle.Entities {
			evidence := buildEvidenceRefs(svc.analyzer, e.FactIDs, e.EvidenceIDs)
			switch e.Type {
			case "Dependency":
				name := strings.TrimSpace(fmt.Sprint(e.Attributes["name"]))
				if name == "" {
					continue
				}
				version := strings.TrimSpace(fmt.Sprint(e.Attributes["version"]))
				if version == "" {
					version = "unknown"
				}
				label := name + "@" + version
				depNode := dependencyNodeID(svc.spec.ID, e.ID)
				depNodesByName[strings.ToLower(name)] = depNode
				g.addNode(graphschema.Node{
					ID:        depNode,
					Type:      "dependency",
					Label:     label,
					ServiceID: svc.spec.ID,
					Attributes: map[string]any{
						"name":        name,
						"version":     version,
						"ecosystem":   e.Attributes["ecosystem"],
						"scope":       e.Attributes["scope"],
						"internal":    e.Attributes["internal"],
						"source_file": e.Attributes["source_file"],
					},
					Confidence: e.Confidence,
					Inferred:   false,
				})
				g.addEdge(graphschema.Edge{
					ID:           edgeID("service_depends_on_dependency", serviceNode, depNode, name+"|"+version),
					Type:         "service_depends_on_dependency",
					SourceID:     serviceNode,
					TargetID:     depNode,
					Attributes:   map[string]any{"name": name, "version": version, "ecosystem": e.Attributes["ecosystem"]},
					Confidence:   e.Confidence,
					Inferred:     false,
					EvidenceRefs: evidence,
				})

				srcPath := strings.TrimSpace(fmt.Sprint(e.Attributes["source_file"]))
				for _, rule := range ownershipRules {
					owner := strings.TrimSpace(fmt.Sprint(rule.attributes["owner"]))
					pattern := strings.TrimSpace(fmt.Sprint(rule.attributes["pattern"]))
					if owner == "" || pattern == "" || !ownershipPatternMatch(pattern, srcPath) {
						continue
					}
					ownerNode := ownerNodeID(svc.spec.ID, owner)
					g.addNode(graphschema.Node{
						ID:        ownerNode,
						Type:      "owner",
						Label:     owner,
						ServiceID: svc.spec.ID,
						Attributes: map[string]any{
							"owner": owner,
						},
						Confidence: rule.confidence,
						Inferred:   false,
					})
					ruleEvidence := buildEvidenceRefs(svc.analyzer, rule.factIDs, rule.evidenceIDs)
					g.addEdge(graphschema.Edge{
						ID:           edgeID("dependency_owned_by", depNode, ownerNode, name+"|"+owner),
						Type:         "dependency_owned_by",
						SourceID:     depNode,
						TargetID:     ownerNode,
						Attributes:   map[string]any{"pattern": pattern, "owner": owner},
						Confidence:   rule.confidence,
						Inferred:     false,
						EvidenceRefs: ruleEvidence,
					})
				}
			case "DependencyRisk":
				name := strings.TrimSpace(fmt.Sprint(e.Attributes["name"]))
				riskType := strings.TrimSpace(fmt.Sprint(e.Attributes["risk_type"]))
				if riskType == "" {
					riskType = "dependency_risk"
				}
				riskNode := dependencyRiskNodeID(svc.spec.ID, e.ID)
				label := riskType
				if name != "" {
					label += ": " + name
				}
				g.addNode(graphschema.Node{
					ID:        riskNode,
					Type:      "dependency_risk",
					Label:     label,
					ServiceID: svc.spec.ID,
					Attributes: map[string]any{
						"name":      name,
						"risk_type": riskType,
						"severity":  e.Attributes["severity"],
						"reason":    e.Attributes["reason"],
						"version":   e.Attributes["version"],
						"ecosystem": e.Attributes["ecosystem"],
					},
					Confidence: e.Confidence,
					Inferred:   false,
				})
				g.addEdge(graphschema.Edge{
					ID:           edgeID("service_has_dependency_risk", serviceNode, riskNode, name+"|"+riskType),
					Type:         "service_has_dependency_risk",
					SourceID:     serviceNode,
					TargetID:     riskNode,
					Attributes:   map[string]any{"name": name, "risk_type": riskType},
					Confidence:   e.Confidence,
					Inferred:     false,
					EvidenceRefs: evidence,
				})
				if depNode, ok := depNodesByName[strings.ToLower(name)]; ok {
					g.addEdge(graphschema.Edge{
						ID:           edgeID("dependency_has_risk", depNode, riskNode, name+"|"+riskType),
						Type:         "dependency_has_risk",
						SourceID:     depNode,
						TargetID:     riskNode,
						Attributes:   map[string]any{"name": name, "risk_type": riskType},
						Confidence:   e.Confidence,
						Inferred:     false,
						EvidenceRefs: evidence,
					})
				}
			}
		}
	}
}

func (g *graphBuilder) resolveCrossRepoCanonicalization() {
	g.resolveCanonicalServices()
	g.resolveCanonicalQueuesAndDatabases()
	g.resolveCanonicalAPIHosts()
}

func (g *graphBuilder) resolveConfidenceAndConflictEdges() {
	for i := range g.services {
		svc := &g.services[i]
		serviceNode := serviceNodeID(svc.spec.ID)
		for _, e := range svc.bundle.Entities {
			if e.Type != "Conflict" {
				continue
			}
			nID := conflictNodeID(svc.spec.ID, e.ID)
			label := strings.TrimSpace(fmt.Sprint(e.Attributes["entity_type"]))
			if label == "" {
				label = "conflict"
			} else {
				label = "conflict: " + label
			}
			g.addNode(graphschema.Node{
				ID:        nID,
				Type:      "conflict",
				Label:     label,
				ServiceID: svc.spec.ID,
				Attributes: map[string]any{
					"entity_type":        e.Attributes["entity_type"],
					"entity_natural_key": e.Attributes["entity_natural_key"],
					"conflict_keys":      e.Attributes["conflict_keys"],
					"observed_values":    e.Attributes["observed_values"],
					"status":             e.Attributes["status"],
					"severity":           e.Attributes["severity"],
				},
				Confidence: e.Confidence,
				Inferred:   false,
			})
			evidence := buildEvidenceRefs(svc.analyzer, e.FactIDs, e.EvidenceIDs)
			g.addEdge(graphschema.Edge{
				ID:           edgeID("service_has_conflict", serviceNode, nID, e.ID),
				Type:         "service_has_conflict",
				SourceID:     serviceNode,
				TargetID:     nID,
				Attributes:   map[string]any{"status": e.Attributes["status"]},
				Confidence:   e.Confidence,
				Inferred:     false,
				EvidenceRefs: evidence,
			})
		}
	}
}

func (g *graphBuilder) resolveVerificationDecisionEdges() {
	for i := range g.services {
		svc := &g.services[i]
		serviceNode := serviceNodeID(svc.spec.ID)
		for _, e := range svc.bundle.Entities {
			if e.Type != "VerificationDecision" {
				continue
			}
			nID := verificationDecisionNodeID(svc.spec.ID, e.ID)
			status := strings.TrimSpace(fmt.Sprint(e.Attributes["status"]))
			if status == "" {
				status = "needs_review"
			}
			label := "verification: " + status
			g.addNode(graphschema.Node{
				ID:        nID,
				Type:      "verification_decision",
				Label:     label,
				ServiceID: svc.spec.ID,
				Attributes: map[string]any{
					"subject_entity_id":   e.Attributes["subject_entity_id"],
					"subject_entity_type": e.Attributes["subject_entity_type"],
					"status":              status,
					"reason":              e.Attributes["reason"],
					"verifier_id":         e.Attributes["verifier_id"],
					"verifier_version":    e.Attributes["verifier_version"],
					"created_at_utc":      e.Attributes["created_at_utc"],
				},
				Confidence: e.Confidence,
				Inferred:   false,
			})
			evidence := buildEvidenceRefs(svc.analyzer, e.FactIDs, e.EvidenceIDs)
			g.addEdge(graphschema.Edge{
				ID:           edgeID("service_has_verification_decision", serviceNode, nID, e.ID),
				Type:         "service_has_verification_decision",
				SourceID:     serviceNode,
				TargetID:     nID,
				Attributes:   map[string]any{"status": status},
				Confidence:   e.Confidence,
				Inferred:     false,
				EvidenceRefs: evidence,
			})

			subjectType := strings.TrimSpace(fmt.Sprint(e.Attributes["subject_entity_type"]))
			subjectID := strings.TrimSpace(fmt.Sprint(e.Attributes["subject_entity_id"]))
			if targetNode := graphNodeIDForEntity(svc.spec.ID, subjectType, subjectID); targetNode != "" {
				g.addEdge(graphschema.Edge{
					ID:         edgeID("verification_decision_targets_entity", nID, targetNode, e.ID),
					Type:       "verification_decision_targets_entity",
					SourceID:   nID,
					TargetID:   targetNode,
					Attributes: map[string]any{"subject_entity_type": subjectType, "subject_entity_id": subjectID},
					Confidence: e.Confidence,
					Inferred:   false,
				})
			}
		}
	}
}

func (g *graphBuilder) resolveCanonicalServices() {
	groups := map[string][]serviceInput{}
	for _, svc := range g.services {
		keys := canonicalServiceKeys(svc.spec)
		for _, key := range keys {
			groups[key] = append(groups[key], svc)
		}
	}

	seenService := map[string]struct{}{}
	for key, members := range groups {
		unique := dedupeServices(members)
		if len(unique) < 2 {
			continue
		}
		buckets := map[string][]serviceInput{}
		for _, svc := range unique {
			scope := serviceEnvironmentScope(svc.envTags)
			buckets[scope] = append(buckets[scope], svc)
		}
		for scope, scopedMembers := range buckets {
			scopedMembers = dedupeServices(scopedMembers)
			if len(scopedMembers) < 2 {
				continue
			}
			canonNodeID := canonicalServiceNodeIDScoped(key, scope)
			label := key
			if scope != "unknown" {
				label = key + " [" + scope + "]"
			}
			g.addNode(graphschema.Node{
				ID:    canonNodeID,
				Type:  "canonical_service",
				Label: label,
				Attributes: map[string]any{
					"canonical_key": key,
					"member_count":  len(scopedMembers),
					"env_scope":     scope,
					"member_services": func() []string {
						ids := make([]string, 0, len(scopedMembers))
						for _, m := range scopedMembers {
							ids = append(ids, m.spec.ID)
						}
						sort.Strings(ids)
						return ids
					}(),
				},
				Confidence: 0.85,
				Inferred:   true,
			})
			for _, svc := range scopedMembers {
				svcNodeID := serviceNodeID(svc.spec.ID)
				if _, ok := seenService[svcNodeID+"|"+canonNodeID]; ok {
					continue
				}
				seenService[svcNodeID+"|"+canonNodeID] = struct{}{}
				g.addEdge(graphschema.Edge{
					ID:       edgeID("service_alias_of_canonical_service", svcNodeID, canonNodeID, key+"|"+scope),
					Type:     "service_alias_of_canonical_service",
					SourceID: svcNodeID,
					TargetID: canonNodeID,
					Attributes: map[string]any{
						"canonical_key":     key,
						"env_scope":         scope,
						"source_service_id": svc.spec.ID,
						"source_repo_path":  svc.spec.RepoPath,
					},
					Confidence: 0.85,
					Inferred:   true,
				})
			}
		}
	}
}

func (g *graphBuilder) resolveCanonicalQueuesAndDatabases() {
	queueGroups := map[string][]nodeRef{}
	dbGroups := map[string][]nodeRef{}

	for _, n := range g.nodeByID {
		switch n.Type {
		case "queue":
			key := canonicalQueueKey(n.Label)
			if key != "" {
				queueGroups[key] = append(queueGroups[key], nodeRef{id: n.ID, label: n.Label, typ: n.Type})
			}
		case "database":
			key := canonicalDatabaseKey(n.Label)
			if key != "" {
				dbGroups[key] = append(dbGroups[key], nodeRef{id: n.ID, label: n.Label, typ: n.Type})
			}
		}
	}

	for key, nodes := range queueGroups {
		nodes = dedupeNodeRefs(nodes)
		if len(nodes) < 2 {
			continue
		}
		canonNodeID := canonicalQueueNodeID(key)
		g.addNode(graphschema.Node{
			ID:         canonNodeID,
			Type:       "canonical_queue",
			Label:      key,
			Attributes: map[string]any{"canonical_key": key, "member_count": len(nodes)},
			Confidence: 0.8,
			Inferred:   true,
		})
		for _, n := range nodes {
			g.addEdge(graphschema.Edge{
				ID:         edgeID("queue_alias_of_canonical_queue", n.id, canonNodeID, key),
				Type:       "queue_alias_of_canonical_queue",
				SourceID:   n.id,
				TargetID:   canonNodeID,
				Attributes: map[string]any{"canonical_key": key},
				Confidence: 0.8,
				Inferred:   true,
			})
		}
	}

	for key, nodes := range dbGroups {
		nodes = dedupeNodeRefs(nodes)
		if len(nodes) < 2 {
			continue
		}
		canonNodeID := canonicalDatabaseNodeID(key)
		g.addNode(graphschema.Node{
			ID:         canonNodeID,
			Type:       "canonical_database",
			Label:      key,
			Attributes: map[string]any{"canonical_key": key, "member_count": len(nodes)},
			Confidence: 0.8,
			Inferred:   true,
		})
		for _, n := range nodes {
			g.addEdge(graphschema.Edge{
				ID:         edgeID("database_alias_of_canonical_database", n.id, canonNodeID, key),
				Type:       "database_alias_of_canonical_database",
				SourceID:   n.id,
				TargetID:   canonNodeID,
				Attributes: map[string]any{"canonical_key": key},
				Confidence: 0.8,
				Inferred:   true,
			})
		}
	}
}

func (g *graphBuilder) resolveCanonicalAPIHosts() {
	hostToServiceSources := map[string]map[string]map[string]struct{}{}
	addHost := func(host string, serviceID string, sourceKind string) {
		host = canonicalHost(host)
		if host == "" || serviceID == "" || sourceKind == "" {
			return
		}
		services, ok := hostToServiceSources[host]
		if !ok {
			services = map[string]map[string]struct{}{}
			hostToServiceSources[host] = services
		}
		kinds, ok := services[serviceID]
		if !ok {
			kinds = map[string]struct{}{}
			services[serviceID] = kinds
		}
		kinds[sourceKind] = struct{}{}
	}

	for i := range g.services {
		svc := &g.services[i]
		for _, baseURL := range svc.spec.BaseURLs {
			addHost(baseURL, svc.spec.ID, "base_url")
		}
		for _, e := range svc.bundle.Entities {
			if e.Type != "ExternalCall" {
				continue
			}
			protocol := strings.ToLower(strings.TrimSpace(fmt.Sprint(e.Attributes["protocol"])))
			if protocol != "" && protocol != "http" {
				continue
			}
			rawTarget := strings.TrimSpace(fmt.Sprint(e.Attributes["target"]))
			target := g.resolveExternalCallTarget(svc, rawTarget)
			addHost(target, svc.spec.ID, "external_call")
		}
	}

	for host, serviceSources := range hostToServiceSources {
		if len(serviceSources) < 2 {
			continue
		}
		nodeID := canonicalAPIHostNodeID(host)
		g.addNode(graphschema.Node{
			ID:    nodeID,
			Type:  "canonical_api_host",
			Label: host,
			Attributes: map[string]any{
				"canonical_host": host,
				"member_count":   len(serviceSources),
				"member_services": func() []string {
					ids := make([]string, 0, len(serviceSources))
					for id := range serviceSources {
						ids = append(ids, id)
					}
					sort.Strings(ids)
					return ids
				}(),
			},
			Confidence: 0.85,
			Inferred:   true,
		})
		for serviceID, kinds := range serviceSources {
			serviceNode := serviceNodeID(serviceID)
			kindList := make([]string, 0, len(kinds))
			for k := range kinds {
				kindList = append(kindList, k)
			}
			sort.Strings(kindList)
			g.addEdge(graphschema.Edge{
				ID:       edgeID("service_alias_of_canonical_api_host", serviceNode, nodeID, host+"|"+strings.Join(kindList, ",")),
				Type:     "service_alias_of_canonical_api_host",
				SourceID: serviceNode,
				TargetID: nodeID,
				Attributes: map[string]any{
					"canonical_host":    host,
					"sources":           kindList,
					"source_service_id": serviceID,
					"source_repo_path":  g.serviceRepoPath(serviceID),
				},
				Confidence: 0.85,
				Inferred:   true,
			})
		}
	}
}

type bundleioEntityRef struct {
	id          string
	attributes  map[string]any
	confidence  float64
	factIDs     []string
	evidenceIDs []string
}

func (g *graphBuilder) matchTargetService(target string, sourceServiceID string) *serviceInput {
	svc, _ := g.matchTargetServiceDetailed(target, sourceServiceID)
	return svc
}

func (g *graphBuilder) matchTargetServiceDetailed(target string, sourceServiceID string) (*serviceInput, []string) {
	normalizedTarget := normalizeTarget(target)
	if normalizedTarget == "" {
		return nil, nil
	}
	sourceEnv := g.serviceEnvTags(sourceServiceID)
	var best *serviceInput
	bestScore := -1
	tied := []string{}
	for i := range g.services {
		svc := &g.services[i]
		if svc.spec.ID == sourceServiceID {
			continue
		}
		if !environmentsCompatible(sourceEnv, svc.envTags) {
			continue
		}
		for _, u := range svc.spec.BaseURLs {
			if u == "" {
				continue
			}
			candidate := normalizeTarget(u)
			if candidate == "" || !strings.Contains(normalizedTarget, candidate) {
				continue
			}
			score := len(candidate)
			if canonicalHost(normalizedTarget) != "" && canonicalHost(normalizedTarget) == canonicalHost(candidate) {
				score += 1000
			}
			if score > bestScore {
				best = svc
				bestScore = score
				tied = []string{svc.spec.ID}
				continue
			}
			if score == bestScore {
				tied = append(tied, svc.spec.ID)
			}
		}
	}
	tied = uniqueStrings(tied)
	if len(tied) > 1 {
		return nil, tied
	}
	return best, tied
}

func (g *graphBuilder) inferTargetService(target string, sourceServiceID string) *serviceInput {
	svc, _ := g.inferTargetServiceDetailed(target, sourceServiceID)
	return svc
}

func (g *graphBuilder) inferTargetServiceDetailed(target string, sourceServiceID string) (*serviceInput, []string) {
	normalizedTarget := normalizeTarget(target)
	if normalizedTarget == "" {
		return nil, nil
	}
	sourceEnv := g.serviceEnvTags(sourceServiceID)
	var best *serviceInput
	bestScore := -1
	tied := []string{}
	for i := range g.services {
		svc := &g.services[i]
		if svc.spec.ID == sourceServiceID {
			continue
		}
		if !environmentsCompatible(sourceEnv, svc.envTags) {
			continue
		}
		for _, alias := range serviceAliases(svc.spec) {
			score := aliasMatchScore(normalizedTarget, alias)
			if score <= 0 {
				continue
			}
			if score > bestScore {
				best = svc
				bestScore = score
				tied = []string{svc.spec.ID}
				continue
			}
			if score == bestScore {
				tied = append(tied, svc.spec.ID)
			}
		}
	}
	tied = uniqueStrings(tied)
	if len(tied) > 1 {
		return nil, tied
	}
	return best, tied
}

func (g *graphBuilder) matchTargetEndpoint(targetService serviceInput, method string, target string) string {
	targetPath := normalizePath(target)
	if targetPath == "" {
		return ""
	}
	for _, e := range targetService.bundle.Entities {
		if e.Type != "Endpoint" {
			continue
		}
		eMethod := strings.ToUpper(strings.TrimSpace(fmt.Sprint(e.Attributes["method"])))
		ePath := normalizePath(fmt.Sprint(e.Attributes["path"]))
		if ePath == "" {
			continue
		}
		if method != "UNKNOWN" && method != eMethod {
			continue
		}
		if pathMatches(targetPath, ePath) {
			return endpointNodeID(targetService.spec.ID, e.ID)
		}
	}
	return ""
}

func (g *graphBuilder) addNode(n graphschema.Node) {
	if _, exists := g.nodeByID[n.ID]; exists {
		return
	}
	if n.Attributes == nil {
		n.Attributes = map[string]any{}
	}
	g.nodeByID[n.ID] = n
	g.byTypeNode[n.Type]++
}

func (g *graphBuilder) addEdge(e graphschema.Edge) {
	if _, exists := g.edgeByID[e.ID]; exists {
		return
	}
	if e.Attributes == nil {
		e.Attributes = map[string]any{}
	}
	g.edgeByID[e.ID] = e
	g.byTypeEdge[e.Type]++
}

func buildEvidenceRefs(bundle facts.Bundle, factIDs []string, evidenceIDs []string) []graphschema.EvidenceRef {
	evidenceByID := map[string]facts.Evidence{}
	for _, e := range bundle.Evidence {
		evidenceByID[e.ID] = e
	}

	factSet := map[string]struct{}{}
	for _, id := range factIDs {
		factSet[id] = struct{}{}
	}
	evidenceSet := map[string]struct{}{}
	for _, id := range evidenceIDs {
		evidenceSet[id] = struct{}{}
	}

	refs := make([]graphschema.EvidenceRef, 0, len(evidenceSet))
	for id := range evidenceSet {
		ev, ok := evidenceByID[id]
		if !ok {
			refs = append(refs, graphschema.EvidenceRef{EvidenceID: id})
			continue
		}
		refs = append(refs, graphschema.EvidenceRef{
			SnapshotID: ev.SnapshotID,
			FilePath:   ev.FilePath,
			StartLine:  ev.StartLine,
			StartCol:   ev.StartCol,
			EndLine:    ev.EndLine,
			EndCol:     ev.EndCol,
			EvidenceID: ev.ID,
		})
	}

	sort.Slice(refs, func(i, j int) bool {
		if refs[i].FilePath == refs[j].FilePath {
			return refs[i].EvidenceID < refs[j].EvidenceID
		}
		return refs[i].FilePath < refs[j].FilePath
	})

	// Attach one fact id when available to preserve edge-fact traceability.
	if len(factSet) > 0 && len(refs) > 0 {
		ids := make([]string, 0, len(factSet))
		for id := range factSet {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for i := range refs {
			refs[i].FactID = ids[0]
		}
	}
	return refs
}

func normalizeTarget(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.Trim(v, "\"'")
	return v
}

func aliasMatchScore(target string, alias string) int {
	alias = normalizeTarget(alias)
	if alias == "" {
		return 0
	}
	// Ignore weak aliases to reduce false-positive service links.
	if len(alias) < 4 {
		return 0
	}
	if target == alias {
		return 3000 + len(alias)
	}
	tokens := tokenizeTarget(target)
	for _, tok := range tokens {
		if tok == alias {
			return 2000 + len(alias)
		}
	}
	if strings.Contains(target, alias) {
		return 1000 + len(alias)
	}
	return 0
}

func tokenizeTarget(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.FieldsFunc(v, func(r rune) bool {
		switch r {
		case '/', ':', '.', '-', '_', '?', '&', '=', '#':
			return true
		default:
			return false
		}
	})
	return uniqueStrings(parts)
}

func normalizePath(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, "\"'")
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		u, err := url.Parse(v)
		if err != nil {
			return ""
		}
		v = strings.TrimSpace(u.Path)
	}
	if v == "" {
		return "/"
	}
	if !strings.HasPrefix(v, "/") {
		v = "/" + v
	}
	if len(v) > 1 {
		v = strings.TrimRight(v, "/")
	}
	return v
}

func pathMatches(targetPath string, endpointPath string) bool {
	if targetPath == endpointPath {
		return true
	}
	targetParts := strings.Split(strings.Trim(targetPath, "/"), "/")
	endpointParts := strings.Split(strings.Trim(endpointPath, "/"), "/")
	if len(targetParts) != len(endpointParts) {
		return false
	}
	for i := range targetParts {
		if strings.HasPrefix(endpointParts[i], ":") || strings.HasPrefix(endpointParts[i], "{") {
			continue
		}
		if targetParts[i] != endpointParts[i] {
			return false
		}
	}
	return true
}

func serviceAliases(spec serviceSpec) []string {
	out := []string{
		normalizeTarget(spec.ID),
		normalizeTarget(spec.Name),
		normalizeTarget(filepathBase(spec.RepoPath)),
	}
	return uniqueStrings(out)
}

func filepathBase(v string) string {
	v = strings.ReplaceAll(v, "\\", "/")
	v = strings.Trim(v, "/")
	if v == "" {
		return ""
	}
	parts := strings.Split(v, "/")
	return parts[len(parts)-1]
}

func edgeConfidence(base float64, inferred bool) float64 {
	if !inferred {
		return base
	}
	if base <= 0 {
		return 0.45
	}
	if base > 0.65 {
		return 0.65
	}
	return base
}

func serviceNodeID(serviceID string) string {
	return "svc:" + serviceID
}

func endpointNodeID(serviceID, entityID string) string {
	return "ep:" + serviceID + ":" + entityID
}

func runtimeNodeID(serviceID, entityID string) string {
	return "rt:" + serviceID + ":" + entityID
}

func pipelineNodeID(serviceID, entityID string) string {
	return "pipe:" + serviceID + ":" + entityID
}

func buildArtifactNodeID(serviceID, entityID string) string {
	return "art:" + serviceID + ":" + entityID
}

func deploymentNodeID(serviceID, entityID string) string {
	return "dep:" + serviceID + ":" + entityID
}

func infraResourceNodeID(serviceID, entityID string) string {
	return "infra:" + serviceID + ":" + entityID
}

func dependencyNodeID(serviceID, entityID string) string {
	return "depkg:" + serviceID + ":" + entityID
}

func ownerNodeID(serviceID, owner string) string {
	return "owner:" + serviceID + ":" + stableID(strings.ToLower(strings.TrimSpace(owner)))
}

func dependencyRiskNodeID(serviceID, entityID string) string {
	return "deprisk:" + serviceID + ":" + entityID
}

func conflictNodeID(serviceID, entityID string) string {
	return "conflict:" + serviceID + ":" + entityID
}

func verificationDecisionNodeID(serviceID, entityID string) string {
	return "verify:" + serviceID + ":" + entityID
}

func canonicalServiceNodeID(key string) string {
	return "canon:svc:" + stableID(strings.ToLower(strings.TrimSpace(key)))
}

func canonicalServiceNodeIDScoped(key string, scope string) string {
	if strings.TrimSpace(scope) == "" {
		scope = "unknown"
	}
	return "canon:svc:" + stableID(strings.ToLower(strings.TrimSpace(key+"|"+scope)))
}

func canonicalQueueNodeID(key string) string {
	return "canon:queue:" + stableID(strings.ToLower(strings.TrimSpace(key)))
}

func canonicalDatabaseNodeID(key string) string {
	return "canon:db:" + stableID(strings.ToLower(strings.TrimSpace(key)))
}

func canonicalAPIHostNodeID(host string) string {
	return "canon:host:" + stableID(strings.ToLower(strings.TrimSpace(host)))
}

func configNodeID(serviceID string, key string, env string) string {
	return "cfg:" + serviceID + ":" + stableID(strings.ToLower(strings.TrimSpace(key)+"|"+strings.TrimSpace(env)))
}

func environmentNodeID(env string) string {
	return "env:" + stableID(strings.ToLower(strings.TrimSpace(env)))
}

func sensitiveSurfaceNodeID(serviceID, entityID string) string {
	return "sens:" + serviceID + ":" + entityID
}

func unresolvedAPICallNodeID(serviceID, entityID string) string {
	return "unresolved_api:" + serviceID + ":" + entityID
}

func queueNodeID(topic string) string {
	return "queue:" + stableID(topic)
}

func databaseNodeID(name string) string {
	return "db:" + stableID(name)
}

func edgeID(edgeType, sourceID, targetID, disambiguator string) string {
	return "edge:" + stableID(edgeType+"|"+sourceID+"|"+targetID+"|"+disambiguator)
}

func stableID(v string) string {
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}

func uniqueStrings(values []string) []string {
	set := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, exists := set[v]; exists {
			continue
		}
		set[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func detectServiceEnvironments(spec serviceSpec, entities []bundleio.Entity) map[string]struct{} {
	out := map[string]struct{}{}
	add := func(v string) {
		if n := normalizeEnvironmentTag(v); n != "" {
			out[n] = struct{}{}
		}
	}
	for _, e := range entities {
		switch e.Type {
		case "ConfigKey", "Deployment", "SensitiveSurface":
			add(fmt.Sprint(e.Attributes["environment"]))
		}
	}
	for _, u := range spec.BaseURLs {
		add(envTagFromText(u))
	}
	add(envTagFromText(spec.ID))
	add(envTagFromText(spec.Name))
	add(envTagFromText(spec.RepoPath))
	return out
}

func envTagFromText(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	switch {
	case strings.Contains(s, "production"), strings.Contains(s, "prod"):
		return "prod"
	case strings.Contains(s, "staging"), strings.Contains(s, "stage"), strings.Contains(s, "preprod"):
		return "stage"
	case strings.Contains(s, "development"), strings.Contains(s, "dev"):
		return "dev"
	case strings.Contains(s, "local"):
		return "local"
	case strings.Contains(s, "test"), strings.Contains(s, "qa"):
		return "test"
	default:
		return ""
	}
}

func normalizeEnvironmentTag(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	switch s {
	case "prod", "production":
		return "prod"
	case "staging", "stage", "preprod", "pre-production":
		return "stage"
	case "dev", "development":
		return "dev"
	case "test", "qa":
		return "test"
	case "local":
		return "local"
	default:
		if s == "" {
			return ""
		}
		return envTagFromText(s)
	}
}

func serviceEnvironmentScope(tags map[string]struct{}) string {
	if len(tags) == 0 {
		return "unknown"
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, "+")
}

func environmentsCompatible(source map[string]struct{}, target map[string]struct{}) bool {
	if len(source) == 0 || len(target) == 0 {
		return true
	}
	for k := range source {
		if _, ok := target[k]; ok {
			return true
		}
	}
	return false
}

func (g *graphBuilder) serviceEnvTags(serviceID string) map[string]struct{} {
	for i := range g.services {
		if g.services[i].spec.ID == serviceID {
			return g.services[i].envTags
		}
	}
	return nil
}

func (g *graphBuilder) serviceRepoPath(serviceID string) string {
	for i := range g.services {
		if g.services[i].spec.ID == serviceID {
			return g.services[i].spec.RepoPath
		}
	}
	return ""
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func ownershipPatternMatch(pattern string, path string) bool {
	pattern = strings.TrimSpace(pattern)
	path = strings.TrimSpace(path)
	if pattern == "" || path == "" {
		return false
	}
	p := strings.TrimPrefix(pattern, "/")
	t := strings.TrimPrefix(path, "/")

	if strings.HasSuffix(p, "/") {
		return strings.HasPrefix(t, p)
	}
	if strings.Contains(p, "*") || strings.Contains(p, "?") {
		ok, err := filepath.Match(p, t)
		if err == nil && ok {
			return true
		}
		// fallback for patterns anchored with leading slash
		ok, err = filepath.Match(strings.TrimPrefix(pattern, "/"), t)
		return err == nil && ok
	}
	if strings.HasPrefix(t, p) {
		return true
	}
	return strings.EqualFold(p, filepath.Base(t))
}

func canonicalServiceKeys(spec serviceSpec) []string {
	keys := make([]string, 0, 4+len(spec.BaseURLs))
	add := func(v string) {
		v = strings.TrimSpace(strings.ToLower(v))
		v = strings.TrimPrefix(v, "service-")
		v = strings.TrimSuffix(v, "-service")
		v = strings.TrimSuffix(v, ".service")
		if v == "" {
			return
		}
		keys = append(keys, v)
	}

	add(spec.ID)
	add(spec.Name)
	add(filepathBase(spec.RepoPath))
	for _, u := range spec.BaseURLs {
		host := canonicalHost(u)
		if host != "" {
			keys = append(keys, host)
		}
	}
	return uniqueStrings(keys)
}

func canonicalHost(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		u, err := url.Parse(v)
		if err == nil {
			h := strings.ToLower(strings.TrimSpace(u.Hostname()))
			if h != "" {
				return h
			}
		}
	}
	v = strings.Trim(v, "\"'")
	v = strings.TrimPrefix(strings.ToLower(v), "http://")
	v = strings.TrimPrefix(v, "https://")
	v = strings.Split(v, "/")[0]
	v = strings.Split(v, ":")[0]
	return strings.TrimSpace(v)
}

func canonicalQueueKey(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	s = strings.TrimPrefix(s, "kafka:")
	s = strings.TrimPrefix(s, "rabbitmq:")
	s = strings.TrimPrefix(s, "sqs:")
	s = strings.TrimPrefix(s, "sns:")
	s = strings.TrimPrefix(s, "topic:")
	return strings.TrimSpace(s)
}

func canonicalDatabaseKey(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	s = strings.TrimPrefix(s, "postgres://")
	s = strings.TrimPrefix(s, "mysql://")
	s = strings.TrimPrefix(s, "mongodb://")
	s = strings.TrimPrefix(s, "db:")
	s = strings.Split(s, "?")[0]
	s = strings.Trim(s, "/")
	return strings.TrimSpace(s)
}

func dedupeServices(in []serviceInput) []serviceInput {
	out := make([]serviceInput, 0, len(in))
	seen := map[string]struct{}{}
	for _, s := range in {
		id := strings.TrimSpace(s.spec.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, s)
	}
	return out
}

func dedupeNodeRefs(in []nodeRef) []nodeRef {
	out := make([]nodeRef, 0, len(in))
	seen := map[string]struct{}{}
	for _, n := range in {
		if _, ok := seen[n.id]; ok {
			continue
		}
		seen[n.id] = struct{}{}
		out = append(out, n)
	}
	return out
}

func graphNodeIDForEntity(serviceID string, entityType string, entityID string) string {
	if strings.TrimSpace(entityID) == "" {
		return ""
	}
	switch entityType {
	case "Endpoint":
		return endpointNodeID(serviceID, entityID)
	case "RuntimeUnit":
		return runtimeNodeID(serviceID, entityID)
	case "PipelineStep":
		return pipelineNodeID(serviceID, entityID)
	case "BuildArtifact":
		return buildArtifactNodeID(serviceID, entityID)
	case "Deployment":
		return deploymentNodeID(serviceID, entityID)
	case "InfraResource":
		return infraResourceNodeID(serviceID, entityID)
	case "Dependency":
		return dependencyNodeID(serviceID, entityID)
	case "DependencyRisk":
		return dependencyRiskNodeID(serviceID, entityID)
	case "Conflict":
		return conflictNodeID(serviceID, entityID)
	case "SensitiveSurface":
		return sensitiveSurfaceNodeID(serviceID, entityID)
	case "UnresolvedAPICall":
		return unresolvedAPICallNodeID(serviceID, entityID)
	default:
		return ""
	}
}

func (g *graphBuilder) resolveExternalCallTarget(svc *serviceInput, rawTarget string) string {
	rawTarget = strings.TrimSpace(rawTarget)
	if rawTarget == "" || svc == nil {
		return rawTarget
	}
	cfgKey := strings.TrimSpace(strings.TrimPrefix(rawTarget, "cfg:"))
	if cfgKey == rawTarget || cfgKey == "" {
		return rawTarget
	}
	resolved := preferredConfigResolvedValue(svc.bundle.Entities, cfgKey)
	if strings.TrimSpace(resolved) == "" {
		return rawTarget
	}
	return strings.TrimSpace(resolved)
}

func preferredConfigResolvedValue(entities []bundleio.Entity, key string) string {
	type candidate struct {
		value string
		env   string
	}
	keyLower := strings.ToLower(strings.TrimSpace(key))
	if keyLower == "" {
		return ""
	}
	cands := make([]candidate, 0, 8)
	for _, e := range entities {
		if e.Type != "ConfigKey" {
			continue
		}
		entityKey := strings.ToLower(strings.TrimSpace(fmt.Sprint(e.Attributes["key"])))
		if entityKey != keyLower {
			continue
		}
		resolved := strings.TrimSpace(fmt.Sprint(e.Attributes["resolved_value"]))
		if resolved == "" || strings.EqualFold(resolved, "[REDACTED]") {
			continue
		}
		env := strings.ToLower(strings.TrimSpace(fmt.Sprint(e.Attributes["environment"])))
		if env == "" {
			env = "default"
		}
		cands = append(cands, candidate{value: resolved, env: env})
	}
	if len(cands) == 0 {
		return ""
	}
	sort.Slice(cands, func(i, j int) bool {
		pi := envPriority(cands[i].env)
		pj := envPriority(cands[j].env)
		if pi == pj {
			return cands[i].value < cands[j].value
		}
		return pi < pj
	})
	return cands[0].value
}

func envPriority(env string) int {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "prod", "production":
		return 0
	case "staging", "stage", "preprod", "preproduction":
		return 1
	case "local":
		return 2
	case "dev", "development":
		return 3
	case "test", "qa":
		return 4
	case "default":
		return 5
	default:
		return 6
	}
}
