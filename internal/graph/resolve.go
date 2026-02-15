package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"diffmind/internal/facts"
	"diffmind/internal/graphschema"
)

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
			svc.endpointNodes[e.ID] = nodeID
		}
	}
}

func (g *graphBuilder) resolveAPIEdges() {
	byServiceID := map[string]*serviceInput{}
	for i := range g.services {
		byServiceID[g.services[i].spec.ID] = &g.services[i]
	}

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
			target := fmt.Sprint(e.Attributes["target"])
			method := strings.ToUpper(strings.TrimSpace(fmt.Sprint(e.Attributes["method"])))
			if method == "" {
				method = "UNKNOWN"
			}

			var targetSvc *serviceInput
			targetSvc = g.matchTargetService(target, sourceService.spec.ID)
			inferred := false
			if targetSvc == nil {
				targetSvc = g.inferTargetService(target, sourceService.spec.ID)
				inferred = true
			}
			if targetSvc == nil {
				continue
			}

			evidence := buildEvidenceRefs(sourceService.analyzer, e.FactIDs, e.EvidenceIDs)
			serviceToServiceID := edgeID("service_calls_service", srcServiceNode, serviceNodeID(targetSvc.spec.ID), method+"|"+target)
			g.addEdge(graphschema.Edge{
				ID:           serviceToServiceID,
				Type:         "service_calls_service",
				SourceID:     srcServiceNode,
				TargetID:     serviceNodeID(targetSvc.spec.ID),
				Attributes:   map[string]any{"method": method, "target": target, "library": e.Attributes["library"]},
				Confidence:   edgeConfidence(e.Confidence, inferred),
				Inferred:     inferred,
				EvidenceRefs: evidence,
			})

			endpointID := g.matchTargetEndpoint(*targetSvc, method, target)
			if endpointID == "" {
				continue
			}
			serviceToEndpointID := edgeID("service_calls_endpoint", srcServiceNode, endpointID, method+"|"+target)
			g.addEdge(graphschema.Edge{
				ID:           serviceToEndpointID,
				Type:         "service_calls_endpoint",
				SourceID:     srcServiceNode,
				TargetID:     endpointID,
				Attributes:   map[string]any{"method": method, "target": target},
				Confidence:   edgeConfidence(e.Confidence, inferred),
				Inferred:     inferred,
				EvidenceRefs: evidence,
			})
		}
	}
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
			target := strings.TrimSpace(fmt.Sprint(e.Attributes["target"]))
			method := strings.ToUpper(strings.TrimSpace(fmt.Sprint(e.Attributes["method"])))
			if target == "" {
				continue
			}
			evidence := buildEvidenceRefs(svc.analyzer, e.FactIDs, e.EvidenceIDs)
			switch protocol {
			case "queue":
				queueID, ok := queueNodes[target]
				if !ok {
					queueID = queueNodeID(target)
					queueNodes[target] = queueID
					g.addNode(graphschema.Node{
						ID:         queueID,
						Type:       "queue",
						Label:      target,
						Attributes: map[string]any{"name": target, "source": "analyzer"},
						Confidence: e.Confidence,
						Inferred:   false,
					})
				}
				if method == "CONSUME" {
					g.addEdge(graphschema.Edge{
						ID:           edgeID("queue_delivers_to_service", queueID, src, target+"|"+method),
						Type:         "queue_delivers_to_service",
						SourceID:     queueID,
						TargetID:     src,
						Attributes:   map[string]any{"topic": target, "source": "analyzer", "library": e.Attributes["library"]},
						Confidence:   e.Confidence,
						Inferred:     false,
						EvidenceRefs: evidence,
					})
				} else {
					g.addEdge(graphschema.Edge{
						ID:           edgeID("service_publishes_queue", src, queueID, target+"|"+method),
						Type:         "service_publishes_queue",
						SourceID:     src,
						TargetID:     queueID,
						Attributes:   map[string]any{"topic": target, "source": "analyzer", "library": e.Attributes["library"]},
						Confidence:   e.Confidence,
						Inferred:     false,
						EvidenceRefs: evidence,
					})
				}
			case "db":
				dbID, ok := dbNodes[target]
				if !ok {
					dbID = databaseNodeID(target)
					dbNodes[target] = dbID
					g.addNode(graphschema.Node{
						ID:         dbID,
						Type:       "database",
						Label:      target,
						Attributes: map[string]any{"name": target, "source": "analyzer"},
						Confidence: e.Confidence,
						Inferred:   false,
					})
				}
				if method == "READ" {
					g.addEdge(graphschema.Edge{
						ID:           edgeID("service_reads_db", src, dbID, target+"|"+method),
						Type:         "service_reads_db",
						SourceID:     src,
						TargetID:     dbID,
						Attributes:   map[string]any{"database": target, "source": "analyzer", "library": e.Attributes["library"]},
						Confidence:   e.Confidence,
						Inferred:     false,
						EvidenceRefs: evidence,
					})
				} else {
					g.addEdge(graphschema.Edge{
						ID:           edgeID("service_writes_db", src, dbID, target+"|"+method),
						Type:         "service_writes_db",
						SourceID:     src,
						TargetID:     dbID,
						Attributes:   map[string]any{"database": target, "source": "analyzer", "library": e.Attributes["library"]},
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
			qNodeID := queueNodeID(topic)
			queueNodes[topic] = qNodeID
			g.addNode(graphschema.Node{
				ID:         qNodeID,
				Type:       "queue",
				Label:      topic,
				Attributes: map[string]any{"name": topic},
				Confidence: 1.0,
				Inferred:   false,
			})
			g.addEdge(graphschema.Edge{
				ID:         edgeID("service_publishes_queue", src, qNodeID, topic),
				Type:       "service_publishes_queue",
				SourceID:   src,
				TargetID:   qNodeID,
				Attributes: map[string]any{"topic": topic, "source": "manifest"},
				Confidence: 1.0,
				Inferred:   false,
			})
		}
		for _, topic := range uniqueStrings(svc.spec.QueueConsumes) {
			qNodeID, ok := queueNodes[topic]
			if !ok {
				qNodeID = queueNodeID(topic)
				queueNodes[topic] = qNodeID
				g.addNode(graphschema.Node{
					ID:         qNodeID,
					Type:       "queue",
					Label:      topic,
					Attributes: map[string]any{"name": topic},
					Confidence: 1.0,
					Inferred:   false,
				})
			}
			g.addEdge(graphschema.Edge{
				ID:         edgeID("queue_delivers_to_service", qNodeID, src, topic),
				Type:       "queue_delivers_to_service",
				SourceID:   qNodeID,
				TargetID:   src,
				Attributes: map[string]any{"topic": topic, "source": "manifest"},
				Confidence: 1.0,
				Inferred:   false,
			})
		}

		for _, db := range uniqueStrings(svc.spec.DBReads) {
			dbNodeID, ok := dbNodes[db]
			if !ok {
				dbNodeID = databaseNodeID(db)
				dbNodes[db] = dbNodeID
				g.addNode(graphschema.Node{
					ID:         dbNodeID,
					Type:       "database",
					Label:      db,
					Attributes: map[string]any{"name": db},
					Confidence: 1.0,
					Inferred:   false,
				})
			}
			g.addEdge(graphschema.Edge{
				ID:         edgeID("service_reads_db", src, dbNodeID, db),
				Type:       "service_reads_db",
				SourceID:   src,
				TargetID:   dbNodeID,
				Attributes: map[string]any{"database": db, "source": "manifest"},
				Confidence: 1.0,
				Inferred:   false,
			})
		}
		for _, db := range uniqueStrings(svc.spec.DBWrites) {
			dbNodeID, ok := dbNodes[db]
			if !ok {
				dbNodeID = databaseNodeID(db)
				dbNodes[db] = dbNodeID
				g.addNode(graphschema.Node{
					ID:         dbNodeID,
					Type:       "database",
					Label:      db,
					Attributes: map[string]any{"name": db},
					Confidence: 1.0,
					Inferred:   false,
				})
			}
			g.addEdge(graphschema.Edge{
				ID:         edgeID("service_writes_db", src, dbNodeID, db),
				Type:       "service_writes_db",
				SourceID:   src,
				TargetID:   dbNodeID,
				Attributes: map[string]any{"database": db, "source": "manifest"},
				Confidence: 1.0,
				Inferred:   false,
			})
		}
	}
}

func (g *graphBuilder) matchTargetService(target string, sourceServiceID string) *serviceInput {
	normalizedTarget := normalizeTarget(target)
	for i := range g.services {
		svc := &g.services[i]
		if svc.spec.ID == sourceServiceID {
			continue
		}
		for _, u := range svc.spec.BaseURLs {
			if u == "" {
				continue
			}
			if strings.Contains(normalizedTarget, normalizeTarget(u)) {
				return svc
			}
		}
	}
	return nil
}

func (g *graphBuilder) inferTargetService(target string, sourceServiceID string) *serviceInput {
	normalizedTarget := normalizeTarget(target)
	for i := range g.services {
		svc := &g.services[i]
		if svc.spec.ID == sourceServiceID {
			continue
		}
		for _, alias := range serviceAliases(svc.spec) {
			if alias != "" && strings.Contains(normalizedTarget, alias) {
				return svc
			}
		}
	}
	return nil
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
		return u.Path
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
