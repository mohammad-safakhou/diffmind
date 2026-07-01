package artifacts

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/serviceconfig"
	"github.com/mohammad-safakhou/protocol"
)

const DiffMind protocolServiceJSON = ".diffmind/context/service.json"
const DiffMind protocolServiceYAML = "diffmind.yaml"

func protocolEncodeJSON(w io.Writer, doc *protocol.Document) error { return protocol.EncodeJSON(w, doc) }
func protocolEncodeYAML(w io.Writer, doc *protocol.Document) error { return protocol.EncodeYAML(w, doc) }

type protocolBuilder struct {
	doc        *protocol.Document
	oldToNew   map[string]string
	evSeen     map[string]string
	objects    map[string]bool
	resources  map[string]string
	flowedFrom map[string]bool
	pipeline   string
	now        time.Time
}

func buildDiffMind protocol(in WriteInput, manifest model.RunManifest) (*protocol.Document, error) {
	cfg, err := serviceconfig.Load(in.RepoPath)
	if err != nil {
		return nil, err
	}
	serviceName := firstNonEmpty(cfg.Service.Name, cfg.Service.ID, detectedServiceName(in.RepoPath, in.RepoFacts), filepath.Base(in.RepoPath))
	serviceID := semanticIDPart(firstNonEmpty(cfg.Service.ID, serviceName))
	now := in.FinishedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	pipeline := firstNonEmpty(manifest.Pipeline, in.Pipeline, "deterministic")
	b := &protocolBuilder{
		doc: &protocol.Document{
			Schema: protocol.SchemaServiceV1,
			Service: protocol.Service{
				ID:          serviceID,
				Name:        serviceName,
				Team:        firstNonEmpty(cfg.Service.Team, manifest.Team),
				Domain:      cfg.Service.Domain,
				Criticality: cfg.Service.Criticality,
			},
			Repository: protocol.Repository{
				Provider: providerFromRemote(manifest.RepoGitRemoteURL),
				URL:      manifest.RepoGitRemoteURL,
				Branch:   manifest.RepoGitBranch,
				Commit:   manifest.RepoGitSHA,
				Dirty:    manifest.RepoGitDirty,
				Path:     in.RepoPath,
			},
			Metadata: protocol.Metadata{
				GeneratedBy: "diffmind",
				GeneratedAt: now,
				Labels: map[string]string{
					"diffmind_run_id": in.RunID,
					"pipeline":        pipeline,
					"schema_version":  protocol.SchemaServiceV1,
				},
			},
		},
		oldToNew:   map[string]string{},
		evSeen:     map[string]string{},
		objects:    map[string]bool{},
		resources:  map[string]string{},
		flowedFrom: map[string]bool{},
		pipeline:   pipeline,
		now:        now,
	}
	for _, exp := range in.Exposures {
		b.addExposure(exp)
	}
	for _, dep := range in.Dependencies {
		b.addDependency(dep)
	}
	for _, conn := range in.Connections {
		b.addFlow(conn)
	}
	for _, exp := range in.Exposures {
		b.addEntrypointTrace(exp)
	}
	sortDiffMind protocol(b.doc)
	canonicalizeDiffMind protocol(b.doc)
	return b.doc, protocol.ValidateCanonical(b.doc)
}

func (b *protocolBuilder) addExposure(exp model.Exposure) {
	base := exp.BaseEntity
	objectID := semanticObjectID(base)
	if b.objects[objectID] {
		b.oldToNew[base.ID] = objectID
		b.mergeDuplicateObjectiveEvidence(objectID, base, "deterministic")
		return
	}
	b.objects[objectID] = true
	common := b.objectiveBase(base, kindForType(base.Type), "deterministic")
	b.oldToNew[base.ID] = common.ID
	switch base.Type {
	case "http_route", "webhook":
		method, path := methodPath(base)
		b.doc.Objects.HTTPEndpoints = append(b.doc.Objects.HTTPEndpoints, protocol.HTTPEndpoint{
			ObjectiveBase: common,
			Method:        method,
			Path:          path,
			Visibility:    stringDetail(base.Details, "visibility", "direction"),
			Auth:          authFromDetails(base.Details),
			Inputs:        inputsFromBase(base),
			Responses:     responsesFromDetails(base.Details),
		})
	case "queue_consumer", "stream_consume":
		platform, dest := platformDest(base)
		q := protocol.QueueConsumer{ObjectiveBase: common, Platform: platform, ConsumerGroup: stringDetail(base.Details, "consumer_group", "group")}
		if platform == "kafka" || strings.Contains(platform, "stream") {
			q.Topic = dest
		} else {
			q.Queue = dest
		}
		b.doc.Objects.QueueConsumers = append(b.doc.Objects.QueueConsumers, q)
	case "cli_command":
		b.doc.Objects.CLICommands = append(b.doc.Objects.CLICommands, protocol.CLICommand{
			ObjectiveBase: common,
			Command: map[string]any{
				"binary": firstNonEmpty(stringDetail(base.Details, "binary"), base.Name),
				"args":   anyDetail(base.Details, "arguments", "args", "options"),
				"raw":    stringDetail(base.Details, "command"),
			},
			Inputs: inputsFromBase(base),
		})
	case "scheduled_job":
		b.doc.Objects.Activations = append(b.doc.Objects.Activations, protocol.Activation{
			ObjectiveBase: common,
			Schedule:      firstNonEmpty(stringDetail(base.Details, "schedule", "cron"), base.Operation),
			Timezone:      stringDetail(base.Details, "timezone", "zone"),
			Invokes:       map[string]any{"mode": "scheduled_method", "symbol": stringDetail(base.Details, "handler", "entry_method")},
		})
	case "rpc_endpoint":
		b.doc.Objects.RPCEndpoints = append(b.doc.Objects.RPCEndpoints, protocol.RPCObjective{
			ObjectiveBase: common,
			Protocol:      firstNonEmpty(base.Platform, stringDetail(base.Details, "protocol")),
			Service:       stringDetail(base.Details, "service"),
			Method:        stringDetail(base.Details, "method"),
		})
	default:
		common.Kind = "activation"
		b.doc.Objects.Activations = append(b.doc.Objects.Activations, protocol.Activation{ObjectiveBase: common})
	}
}

func (b *protocolBuilder) addDependency(dep model.Dependency) {
	base := dep.BaseEntity
	objectID := semanticObjectID(base)
	if b.objects[objectID] {
		b.oldToNew[base.ID] = objectID
		b.mergeDuplicateObjectiveEvidence(objectID, base, "deterministic")
		return
	}
	b.objects[objectID] = true
	common := b.objectiveBase(base, kindForType(base.Type), "deterministic")
	b.oldToNew[base.ID] = common.ID
	switch base.Type {
	case "outbound_http":
		method := firstNonEmpty(stringDetail(base.Details, "method"), "GET")
		rawURL := firstNonEmpty(stringDetail(base.Details, "url_template", "url", "target_url", "base_url", "default_url", "endpoint"), base.Instance)
		targetName := firstNonEmpty(targetFromInstance(base.InstanceRef), stringDetail(base.Details, "target_service", "service"), hostFromURL(rawURL), base.Instance)
		b.doc.Objects.HTTPCalls = append(b.doc.Objects.HTTPCalls, protocol.HTTPCall{
			ObjectiveBase: common,
			Method:        method,
			URLTemplate:   rawURL,
			Target:        targetRefWithType(targetName, stringDetail(base.Details, "target_type")),
			Inputs:        inputsFromBase(base),
			Responses:     responsesFromDetails(base.Details),
		})
	case "queue_publish":
		platform, dest := platformDest(base)
		q := protocol.QueuePublisher{ObjectiveBase: common, Platform: platform}
		if platform == "kafka" || platform == "sns" || strings.Contains(platform, "stream") {
			q.Topic = dest
		} else {
			q.Queue = dest
		}
		b.doc.Objects.QueuePublishers = append(b.doc.Objects.QueuePublishers, q)
	case "db_operation":
		resRef := b.ensureDBResource(base)
		target := &protocol.DBQueryTarget{
			ResourceRef: resRef,
			Database:    databaseName(base),
			SchemaName:  stringDetail(base.Details, "schema"),
			Tables:      nonEmptyStrings(dataResource(base)),
		}
		b.doc.Objects.DBQueries = append(b.doc.Objects.DBQueries, protocol.DBQuery{
			ObjectiveBase: common,
			Engine:        firstNonEmpty(base.Platform, stringDetail(base.Details, "database_type", "engine")),
			Operation:     normalizeDBOperation(firstNonEmpty(base.OperationKind, stringDetail(base.Details, "operation_kind", "operation_type", "operation"))),
			Access:        accessForOperation(firstNonEmpty(base.OperationKind, stringDetail(base.Details, "operation_kind", "operation_type", "operation"))),
			Target:        target,
			Query: &protocol.QuerySpec{
				Language:    firstNonEmpty(stringDetail(base.Details, "query_language"), "unknown"),
				Template:    stringDetail(base.Details, "query", "sql", "template"),
				Fingerprint: fingerprint(firstNonEmpty(stringDetail(base.Details, "query", "sql", "template"), base.Name)),
				Redacted:    true,
			},
			ORM: ormFromDetails(base.Details),
			Columns: &protocol.ColumnSpec{
				Reads:  stringSliceDetail(base.Details, "reads", "read_columns"),
				Writes: stringSliceDetail(base.Details, "writes", "write_columns"),
			},
		})
	case "cache_operation":
		platform := cachePlatform(base)
		b.doc.Objects.CacheOperations = append(b.doc.Objects.CacheOperations, protocol.CacheOperation{
			ObjectiveBase: common,
			Platform:      platform,
			Operation:     firstNonEmpty(base.OperationKind, stringDetail(base.Details, "operation", "operation_type"), base.Operation),
			KeyPattern:    stringDetail(base.Details, "key_pattern", "key"),
			Target: map[string]any{
				"cache": firstNonEmpty(stringDetail(base.Details, "cache", "cache_name"), base.Instance),
			},
		})
	case "outbound_rpc":
		targetName := firstNonEmpty(stringDetail(base.Details, "target_service", "service"), base.Instance)
		b.doc.Objects.RPCCalls = append(b.doc.Objects.RPCCalls, protocol.RPCObjective{
			ObjectiveBase: common,
			Protocol:      firstNonEmpty(base.Platform, stringDetail(base.Details, "protocol"), "grpc"),
			Service:       targetName,
			Method:        stringDetail(base.Details, "method"),
			Target:        targetRef(targetName),
		})
	case "command_exec":
		b.doc.Objects.CLICommands = append(b.doc.Objects.CLICommands, protocol.CLICommand{
			ObjectiveBase: common,
			Command:       map[string]any{"raw": firstNonEmpty(stringDetail(base.Details, "command"), base.Name)},
			Inputs:        inputsFromBase(base),
		})
	default:
		b.doc.Objects.ConfigReads = append(b.doc.Objects.ConfigReads, protocol.ConfigRead{ObjectiveBase: common})
	}
}

func (b *protocolBuilder) objectiveBase(base model.BaseEntity, kind, detector string) protocol.ObjectiveBase {
	id := semanticObjectID(base)
	obsRefs, evRefs := b.addObservationsAndEvidence(id, base, detector)
	origin := b.originForBase(base)
	pluginSource := detectorName(base, detector)
	return protocol.ObjectiveBase{
		ID:           id,
		Kind:         kind,
		Name:         base.Name,
		Status:       protocol.StatusConfirmed,
		Confidence:   confidence(base.Confidence),
		Origin:       origin,
		Observations: obsRefs,
		EvidenceRefs: evRefs,
		Metadata: map[string]any{
			"legacy_id":     base.ID,
			"legacy_type":   base.Type,
			"platform":      base.Platform,
			"instance":      base.Instance,
			"summary":       base.Summary,
			"tags":          base.Tags,
			"details":       base.Details,
			"plugin_source": pluginSource,
		},
	}
}

func (b *protocolBuilder) originForBase(base model.BaseEntity) protocol.Origin {
	source := strings.ToLower(strings.TrimSpace(base.PluginSource))
	switch {
	case source == "archfile" || source == "imported" || hasTag(base.Tags, "imported") || hasTag(base.Tags, "archfile"):
		return protocol.OriginImported
	case hasTag(base.Tags, "deterministic") || source == "deterministic" ||
		source == "ast" || strings.HasPrefix(source, "ast_") ||
		strings.HasPrefix(source, "deterministic_") ||
		strings.HasPrefix(source, "framework"):
		return protocol.OriginDeterministic
	case source == "manual" || hasTag(base.Tags, "manual"):
		return protocol.OriginManual
	default:
		return protocol.OriginDeterministic
	}
}

func (b *protocolBuilder) mergeDuplicateObjectiveEvidence(objectID string, base model.BaseEntity, detector string) {
	obsRefs, evRefs := b.addObservationsAndEvidence(objectID, base, detector)
	b.appendObjectiveRefs(objectID, obsRefs, evRefs)
}

func (b *protocolBuilder) appendObjectiveRefs(objectID string, obsRefs, evRefs []string) {
	for i := range b.doc.Objects.HTTPEndpoints {
		if b.doc.Objects.HTTPEndpoints[i].ID == objectID {
			appendObjectiveBaseRefs(&b.doc.Objects.HTTPEndpoints[i].ObjectiveBase, obsRefs, evRefs)
			return
		}
	}
	for i := range b.doc.Objects.HTTPCalls {
		if b.doc.Objects.HTTPCalls[i].ID == objectID {
			appendObjectiveBaseRefs(&b.doc.Objects.HTTPCalls[i].ObjectiveBase, obsRefs, evRefs)
			return
		}
	}
	for i := range b.doc.Objects.DBResources {
		if b.doc.Objects.DBResources[i].ID == objectID {
			appendObjectiveBaseRefs(&b.doc.Objects.DBResources[i].ObjectiveBase, obsRefs, evRefs)
			return
		}
	}
	for i := range b.doc.Objects.DBQueries {
		if b.doc.Objects.DBQueries[i].ID == objectID {
			appendObjectiveBaseRefs(&b.doc.Objects.DBQueries[i].ObjectiveBase, obsRefs, evRefs)
			return
		}
	}
	for i := range b.doc.Objects.QueueConsumers {
		if b.doc.Objects.QueueConsumers[i].ID == objectID {
			appendObjectiveBaseRefs(&b.doc.Objects.QueueConsumers[i].ObjectiveBase, obsRefs, evRefs)
			return
		}
	}
	for i := range b.doc.Objects.QueuePublishers {
		if b.doc.Objects.QueuePublishers[i].ID == objectID {
			appendObjectiveBaseRefs(&b.doc.Objects.QueuePublishers[i].ObjectiveBase, obsRefs, evRefs)
			return
		}
	}
	for i := range b.doc.Objects.RPCEndpoints {
		if b.doc.Objects.RPCEndpoints[i].ID == objectID {
			appendObjectiveBaseRefs(&b.doc.Objects.RPCEndpoints[i].ObjectiveBase, obsRefs, evRefs)
			return
		}
	}
	for i := range b.doc.Objects.RPCCalls {
		if b.doc.Objects.RPCCalls[i].ID == objectID {
			appendObjectiveBaseRefs(&b.doc.Objects.RPCCalls[i].ObjectiveBase, obsRefs, evRefs)
			return
		}
	}
	for i := range b.doc.Objects.CLICommands {
		if b.doc.Objects.CLICommands[i].ID == objectID {
			appendObjectiveBaseRefs(&b.doc.Objects.CLICommands[i].ObjectiveBase, obsRefs, evRefs)
			return
		}
	}
	for i := range b.doc.Objects.Activations {
		if b.doc.Objects.Activations[i].ID == objectID {
			appendObjectiveBaseRefs(&b.doc.Objects.Activations[i].ObjectiveBase, obsRefs, evRefs)
			return
		}
	}
	for i := range b.doc.Objects.CacheOperations {
		if b.doc.Objects.CacheOperations[i].ID == objectID {
			appendObjectiveBaseRefs(&b.doc.Objects.CacheOperations[i].ObjectiveBase, obsRefs, evRefs)
			return
		}
	}
	for i := range b.doc.Objects.ConfigReads {
		if b.doc.Objects.ConfigReads[i].ID == objectID {
			appendObjectiveBaseRefs(&b.doc.Objects.ConfigReads[i].ObjectiveBase, obsRefs, evRefs)
			return
		}
	}
	for i := range b.doc.Objects.FeatureFlags {
		if b.doc.Objects.FeatureFlags[i].ID == objectID {
			appendObjectiveBaseRefs(&b.doc.Objects.FeatureFlags[i].ObjectiveBase, obsRefs, evRefs)
			return
		}
	}
}

func appendObjectiveBaseRefs(base *protocol.ObjectiveBase, obsRefs, evRefs []string) {
	base.Observations = appendUniqueStrings(base.Observations, obsRefs...)
	base.EvidenceRefs = appendUniqueStrings(base.EvidenceRefs, evRefs...)
}

func (b *protocolBuilder) addObservationsAndEvidence(objectID string, base model.BaseEntity, detector string) ([]string, []string) {
	var obsRefs []string
	var evRefs []string
	obsN := b.nextObservationOrdinal(objectID)
	evN := b.nextEvidenceOrdinal(objectID)
	for i, loc := range base.Locations {
		if strings.TrimSpace(loc.File) == "" {
			continue
		}
		obsID := fmt.Sprintf("obs.%s.%d", objectID, obsN)
		obsN++
		obsRefs = append(obsRefs, obsID)
		b.doc.Observations = append(b.doc.Observations, protocol.Observation{
			ID:          obsID,
			ObjectRef:   objectID,
			Perspective: perspectiveFor(base.Type, i),
			Location:    &protocol.Location{File: loc.File, StartLine: loc.StartLine, EndLine: loc.EndLine, Symbol: symbolFromDetails(base.Details)},
			Detector:    detectorName(base, detector),
			Confidence:  confidence(base.Confidence),
		})
	}
	for _, ev := range base.Evidence {
		loc := ev.Location
		key := fmt.Sprintf("%s:%d:%d:%s:%s", loc.File, loc.StartLine, loc.EndLine, ev.Source, ev.Snippet)
		evID, ok := b.evSeen[key]
		if !ok {
			evID = fmt.Sprintf("ev.%s.%d", objectID, evN)
			evN++
			b.evSeen[key] = evID
			b.doc.Evidence = append(b.doc.Evidence, protocol.Evidence{
				ID:          evID,
				Type:        "source_location",
				Source:      sourceForEvidence(ev.Source),
				Detector:    detectorName(base, detector),
				File:        loc.File,
				StartLine:   loc.StartLine,
				EndLine:     loc.EndLine,
				Symbol:      symbolFromDetails(base.Details),
				SnippetHash: fingerprint(ev.Snippet),
				ObservedAt:  b.now,
				Confidence:  confidence(base.Confidence),
			})
		}
		evRefs = append(evRefs, evID)
	}
	if len(evRefs) == 0 {
		for _, loc := range base.Locations {
			if strings.TrimSpace(loc.File) == "" {
				continue
			}
			source := "code"
			if strings.Contains(strings.ToLower(detectorName(base, detector)), "config") {
				source = "config"
			}
			key := fmt.Sprintf("%s:%d:%d:%s:%s", loc.File, loc.StartLine, loc.EndLine, source, detectorName(base, detector))
			evID, ok := b.evSeen[key]
			if !ok {
				evID = fmt.Sprintf("ev.%s.%d", objectID, evN)
				evN++
				b.evSeen[key] = evID
				b.doc.Evidence = append(b.doc.Evidence, protocol.Evidence{
					ID:         evID,
					Type:       "source_location",
					Source:     source,
					Detector:   detectorName(base, detector),
					File:       loc.File,
					StartLine:  loc.StartLine,
					EndLine:    loc.EndLine,
					Symbol:     symbolFromDetails(base.Details),
					ObservedAt: b.now,
					Confidence: confidence(base.Confidence),
				})
			}
			evRefs = append(evRefs, evID)
		}
	}
	return obsRefs, evRefs
}

func (b *protocolBuilder) nextObservationOrdinal(objectID string) int {
	prefix := "obs." + objectID + "."
	n := 1
	for _, obs := range b.doc.Observations {
		if strings.HasPrefix(obs.ID, prefix) {
			n++
		}
	}
	return n
}

func (b *protocolBuilder) nextEvidenceOrdinal(objectID string) int {
	prefix := "ev." + objectID + "."
	n := 1
	for _, ev := range b.doc.Evidence {
		if strings.HasPrefix(ev.ID, prefix) {
			n++
		}
	}
	return n
}

func (b *protocolBuilder) ensureDBResource(base model.BaseEntity) string {
	engine := firstNonEmpty(base.Platform, stringDetail(base.Details, "database_type", "engine"), "database")
	table := dataResource(base)
	database := databaseName(base)
	key := strings.ToLower(engine + "|" + database + "|" + table)
	if id := b.resources[key]; id != "" {
		return id
	}
	id := "db." + slug(firstNonEmpty(database+"."+table, table, database, base.Instance, base.Name))
	for i := range b.doc.Objects.DBResources {
		if b.doc.Objects.DBResources[i].ID != id {
			continue
		}
		b.resources[key] = id
		return id
	}
	b.resources[key] = id
	obsRefs, evRefs := b.addObservationsAndEvidence(id, base, "derived_db_resource")
	common := protocol.ObjectiveBase{
		ID:           id,
		Kind:         "db_resource",
		Name:         firstNonEmpty(table, database, base.Instance, base.Name),
		Status:       protocol.StatusConfirmed,
		Confidence:   confidence(base.Confidence),
		Origin:       protocol.OriginDeterministic,
		Observations: obsRefs,
		EvidenceRefs: evRefs,
		Metadata:     map[string]any{"derived_from": base.ID},
	}
	b.doc.Objects.DBResources = append(b.doc.Objects.DBResources, protocol.DBResource{
		ObjectiveBase: common,
		Engine:        engine,
		ResourceType:  firstNonEmpty(stringDetail(base.Details, "resource_type"), "table"),
		Database:      database,
		SchemaName:    stringDetail(base.Details, "schema"),
		Table:         table,
		Ownership:     "used",
		Data:          map[string]any{"classification": "unknown"},
	})
	return id
}

func (b *protocolBuilder) addFlow(conn model.Connection) {
	from := b.oldToNew[conn.FromExposureID]
	to := b.oldToNew[conn.ToDependencyID]
	if from == "" || to == "" {
		return
	}
	b.flowedFrom[from] = true
	flow := protocol.Flow{
		ID:           "flow." + slug(from+"."+to),
		Kind:         flowKind(conn.FromType, conn.ToType),
		Entrypoint:   from,
		From:         from,
		To:           to,
		Reachability: reachability(conn.Condition),
		Condition:    condition(conn.Condition),
		Status:       protocol.StatusConfirmed,
		Confidence:   confidence(conn.Confidence),
		Origin:       protocol.OriginDeterministic,
	}
	if len(conn.Paths) == 0 {
		b.doc.Flows = append(b.doc.Flows, flow)
		return
	}
	nodes := []protocol.FlowNode{{ID: "n1", Ref: from, Role: "entrypoint"}}
	edges := []protocol.FlowEdge{}
	next := 2
	last := "n1"
	for _, step := range conn.Paths[0].Steps {
		nodeID := fmt.Sprintf("n%d", next)
		next++
		nodes = append(nodes, protocol.FlowNode{ID: nodeID, Symbol: step.To, Role: "call"})
		edges = append(edges, protocol.FlowEdge{From: last, To: nodeID, Reachability: reachability(step.Condition), Condition: condition(step.Condition)})
		last = nodeID
	}
	targetNode := fmt.Sprintf("n%d", next)
	nodes = append(nodes, protocol.FlowNode{ID: targetNode, Ref: to, Role: "action"})
	edges = append(edges, protocol.FlowEdge{From: last, To: targetNode, Reachability: protocol.ReachabilityMust})
	flow.Nodes = nodes
	flow.Edges = edges
	flow.From = ""
	flow.To = ""
	b.doc.Flows = append(b.doc.Flows, flow)
}

func (b *protocolBuilder) addEntrypointTrace(exp model.Exposure) {
	from := b.oldToNew[exp.ID]
	if from == "" || b.flowedFrom[from] {
		return
	}
	flow := protocol.Flow{
		ID:         "flow." + slug(from+".entrypoint"),
		Kind:       "entrypoint_trace",
		Entrypoint: from,
		Nodes: []protocol.FlowNode{{
			ID:   "n1",
			Ref:  from,
			Role: "entrypoint",
		}},
		Status:       protocol.StatusConfirmed,
		Confidence:   confidence(exp.Confidence),
		Origin:       protocol.OriginDeterministic,
		EvidenceRefs: b.evidenceRefsForObject(from),
	}
	b.doc.Flows = append(b.doc.Flows, flow)
}

func (b *protocolBuilder) evidenceRefsForObject(id string) []string {
	for _, o := range b.doc.Objects.HTTPEndpoints {
		if o.ID == id {
			return append([]string(nil), o.EvidenceRefs...)
		}
	}
	for _, o := range b.doc.Objects.QueueConsumers {
		if o.ID == id {
			return append([]string(nil), o.EvidenceRefs...)
		}
	}
	for _, o := range b.doc.Objects.CLICommands {
		if o.ID == id {
			return append([]string(nil), o.EvidenceRefs...)
		}
	}
	for _, o := range b.doc.Objects.Activations {
		if o.ID == id {
			return append([]string(nil), o.EvidenceRefs...)
		}
	}
	for _, o := range b.doc.Objects.RPCEndpoints {
		if o.ID == id {
			return append([]string(nil), o.EvidenceRefs...)
		}
	}
	return nil
}

func semanticObjectID(base model.BaseEntity) string {
	switch base.Type {
	case "http_route", "webhook":
		method, path := methodPath(base)
		return "http." + slug(method+"."+path)
	case "outbound_http":
		method := firstNonEmpty(stringDetail(base.Details, "method"), "GET")
		target := firstNonEmpty(stringDetail(base.Details, "target_service", "service"), hostFromURL(stringDetail(base.Details, "url", "target_url", "base_url")), base.Instance, base.Name)
		return "httpcall." + slug(method+"."+target+"."+stringDetail(base.Details, "path", "endpoint"))
	case "db_operation":
		return "dbq." + slug(firstNonEmpty(normalizeDBOperation(stringDetail(base.Details, "operation_kind", "operation_type", "operation")), base.OperationKind, "query")+"."+dataResource(base))
	case "queue_consumer", "stream_consume":
		_, dest := platformDest(base)
		return "queue.consume_" + slug(firstNonEmpty(dest, base.Name))
	case "queue_publish":
		_, dest := platformDest(base)
		return "queue.publish_" + slug(firstNonEmpty(dest, base.Name))
	case "cache_operation":
		return "cache." + slug(firstNonEmpty(stringDetail(base.Details, "operation"), base.OperationKind, base.Name)+"."+firstNonEmpty(stringDetail(base.Details, "cache", "cache_name"), base.Instance, "cache"))
	case "cli_command", "command_exec":
		return "cmd." + slug(firstNonEmpty(stringDetail(base.Details, "command"), base.Name))
	case "scheduled_job":
		return "act." + slug(firstNonEmpty(stringDetail(base.Details, "handler", "entry_method", "method", "k8s_cronjob_name"), base.Name, stringDetail(base.Details, "schedule", "cron")))
	case "outbound_rpc":
		return "rpccall." + slug(firstNonEmpty(stringDetail(base.Details, "service"), base.Instance, base.Name)+"."+stringDetail(base.Details, "method"))
	case "rpc_endpoint":
		return "rpc." + slug(base.Name)
	default:
		return slug(base.Type) + "." + slug(base.Name)
	}
}

func kindForType(t string) string {
	switch t {
	case "http_route", "webhook":
		return "http_endpoint"
	case "outbound_http":
		return "http_call"
	case "db_operation":
		return "db_query"
	case "queue_consumer", "stream_consume":
		return "queue_consumer"
	case "queue_publish":
		return "queue_publisher"
	case "cli_command":
		return "cli_command"
	case "scheduled_job":
		return "kubernetes_cronjob"
	case "cache_operation":
		return "cache_operation"
	case "outbound_rpc":
		return "rpc_call"
	case "rpc_endpoint":
		return "rpc_endpoint"
	default:
		return t
	}
}

func sortDiffMind protocol(doc *protocol.Document) {
	sort.Slice(doc.Objects.DBResources, func(i, j int) bool { return doc.Objects.DBResources[i].ID < doc.Objects.DBResources[j].ID })
	sort.Slice(doc.Objects.HTTPEndpoints, func(i, j int) bool { return doc.Objects.HTTPEndpoints[i].ID < doc.Objects.HTTPEndpoints[j].ID })
	sort.Slice(doc.Objects.HTTPCalls, func(i, j int) bool { return doc.Objects.HTTPCalls[i].ID < doc.Objects.HTTPCalls[j].ID })
	sort.Slice(doc.Objects.DBQueries, func(i, j int) bool { return doc.Objects.DBQueries[i].ID < doc.Objects.DBQueries[j].ID })
	sort.Slice(doc.Objects.QueueConsumers, func(i, j int) bool { return doc.Objects.QueueConsumers[i].ID < doc.Objects.QueueConsumers[j].ID })
	sort.Slice(doc.Objects.QueuePublishers, func(i, j int) bool { return doc.Objects.QueuePublishers[i].ID < doc.Objects.QueuePublishers[j].ID })
	sort.Slice(doc.Objects.RPCEndpoints, func(i, j int) bool { return doc.Objects.RPCEndpoints[i].ID < doc.Objects.RPCEndpoints[j].ID })
	sort.Slice(doc.Objects.RPCCalls, func(i, j int) bool { return doc.Objects.RPCCalls[i].ID < doc.Objects.RPCCalls[j].ID })
	sort.Slice(doc.Objects.CLICommands, func(i, j int) bool { return doc.Objects.CLICommands[i].ID < doc.Objects.CLICommands[j].ID })
	sort.Slice(doc.Objects.Activations, func(i, j int) bool { return doc.Objects.Activations[i].ID < doc.Objects.Activations[j].ID })
	sort.Slice(doc.Objects.CacheOperations, func(i, j int) bool { return doc.Objects.CacheOperations[i].ID < doc.Objects.CacheOperations[j].ID })
	sort.Slice(doc.Objects.ConfigReads, func(i, j int) bool { return doc.Objects.ConfigReads[i].ID < doc.Objects.ConfigReads[j].ID })
	sort.Slice(doc.Objects.FeatureFlags, func(i, j int) bool { return doc.Objects.FeatureFlags[i].ID < doc.Objects.FeatureFlags[j].ID })
	sort.Slice(doc.Observations, func(i, j int) bool { return doc.Observations[i].ID < doc.Observations[j].ID })
	sort.Slice(doc.Evidence, func(i, j int) bool { return doc.Evidence[i].ID < doc.Evidence[j].ID })
	sort.Slice(doc.Flows, func(i, j int) bool { return doc.Flows[i].ID < doc.Flows[j].ID })
}

func canonicalizeDiffMind protocol(doc *protocol.Document) {
	if doc.Flows == nil {
		doc.Flows = []protocol.Flow{}
	}
	if doc.Observations == nil {
		doc.Observations = []protocol.Observation{}
	}
	if doc.Evidence == nil {
		doc.Evidence = []protocol.Evidence{}
	}
	if doc.Objects.HTTPEndpoints == nil {
		doc.Objects.HTTPEndpoints = []protocol.HTTPEndpoint{}
	}
	if doc.Objects.HTTPCalls == nil {
		doc.Objects.HTTPCalls = []protocol.HTTPCall{}
	}
	if doc.Objects.DBResources == nil {
		doc.Objects.DBResources = []protocol.DBResource{}
	}
	if doc.Objects.DBQueries == nil {
		doc.Objects.DBQueries = []protocol.DBQuery{}
	}
	if doc.Objects.QueueConsumers == nil {
		doc.Objects.QueueConsumers = []protocol.QueueConsumer{}
	}
	if doc.Objects.QueuePublishers == nil {
		doc.Objects.QueuePublishers = []protocol.QueuePublisher{}
	}
	if doc.Objects.RPCEndpoints == nil {
		doc.Objects.RPCEndpoints = []protocol.RPCObjective{}
	}
	if doc.Objects.RPCCalls == nil {
		doc.Objects.RPCCalls = []protocol.RPCObjective{}
	}
	if doc.Objects.CLICommands == nil {
		doc.Objects.CLICommands = []protocol.CLICommand{}
	}
	if doc.Objects.Activations == nil {
		doc.Objects.Activations = []protocol.Activation{}
	}
	if doc.Objects.CacheOperations == nil {
		doc.Objects.CacheOperations = []protocol.CacheOperation{}
	}
	if doc.Objects.ConfigReads == nil {
		doc.Objects.ConfigReads = []protocol.ConfigRead{}
	}
	if doc.Objects.FeatureFlags == nil {
		doc.Objects.FeatureFlags = []protocol.FeatureFlag{}
	}
	for i := range doc.Objects.HTTPEndpoints {
		canonicalizeObjectiveBase(&doc.Objects.HTTPEndpoints[i].ObjectiveBase)
	}
	for i := range doc.Objects.HTTPCalls {
		canonicalizeObjectiveBase(&doc.Objects.HTTPCalls[i].ObjectiveBase)
	}
	for i := range doc.Objects.DBResources {
		canonicalizeObjectiveBase(&doc.Objects.DBResources[i].ObjectiveBase)
	}
	for i := range doc.Objects.DBQueries {
		canonicalizeObjectiveBase(&doc.Objects.DBQueries[i].ObjectiveBase)
	}
	for i := range doc.Objects.QueueConsumers {
		canonicalizeObjectiveBase(&doc.Objects.QueueConsumers[i].ObjectiveBase)
	}
	for i := range doc.Objects.QueuePublishers {
		canonicalizeObjectiveBase(&doc.Objects.QueuePublishers[i].ObjectiveBase)
	}
	for i := range doc.Objects.RPCEndpoints {
		canonicalizeObjectiveBase(&doc.Objects.RPCEndpoints[i].ObjectiveBase)
	}
	for i := range doc.Objects.RPCCalls {
		canonicalizeObjectiveBase(&doc.Objects.RPCCalls[i].ObjectiveBase)
	}
	for i := range doc.Objects.CLICommands {
		canonicalizeObjectiveBase(&doc.Objects.CLICommands[i].ObjectiveBase)
	}
	for i := range doc.Objects.Activations {
		canonicalizeObjectiveBase(&doc.Objects.Activations[i].ObjectiveBase)
	}
	for i := range doc.Objects.CacheOperations {
		canonicalizeObjectiveBase(&doc.Objects.CacheOperations[i].ObjectiveBase)
	}
	for i := range doc.Objects.ConfigReads {
		canonicalizeObjectiveBase(&doc.Objects.ConfigReads[i].ObjectiveBase)
	}
	for i := range doc.Objects.FeatureFlags {
		canonicalizeObjectiveBase(&doc.Objects.FeatureFlags[i].ObjectiveBase)
	}
}

func canonicalizeObjectiveBase(base *protocol.ObjectiveBase) {
	if base.Observations == nil {
		base.Observations = []string{}
	}
	if base.EvidenceRefs == nil {
		base.EvidenceRefs = []string{}
	}
}

func methodPath(base model.BaseEntity) (string, string) {
	method := firstNonEmpty(stringDetail(base.Details, "method"), "ANY")
	path := firstNonEmpty(stringDetail(base.Details, "path", "route", "endpoint"), base.Name)
	fields := strings.Fields(path)
	if len(fields) >= 2 && isHTTPMethod(fields[0]) {
		method = fields[0]
		path = fields[1]
	}
	return strings.ToUpper(method), path
}

func platformDest(base model.BaseEntity) (string, string) {
	platform := firstNonEmpty(base.Platform, stringDetail(base.Details, "platform"), "queue")
	lower := strings.ToLower(platform + " " + stringDetail(base.Details, "source", "stream_arn", "event_source_arn", "event_source"))
	if strings.Contains(lower, "dynamodb") && strings.Contains(lower, "stream") {
		platform = "dynamodb_stream"
	}
	dest := firstNonEmpty(stringDetail(base.Details, "topic", "queue", "queue_name", "destination", "stream", "stream_arn", "event_source_arn", "source", "table", "table_name"), base.Instance, base.Name)
	return strings.ToLower(platform), dest
}

func cachePlatform(base model.BaseEntity) string {
	platform := firstNonEmpty(base.Platform, stringDetail(base.Details, "platform", "cache_type", "database_type", "engine"), "cache")
	lower := strings.ToLower(platform + " " + stringDetail(base.Details, "cache", "cache_name", "resource_name") + " " + base.Instance + " " + base.Name)
	if strings.Contains(lower, "redis") {
		return "redis"
	}
	return strings.ToLower(platform)
}

func dataResource(base model.BaseEntity) string {
	return firstNonEmpty(stringDetail(base.Details, "table", "table_or_entity", "entity", "collection", "index", "resource_name"), base.Instance)
}

func databaseName(base model.BaseEntity) string {
	return firstNonEmpty(
		nonUnknown(stringDetail(base.Details, "database_name", "database")),
		nonUnknown(instanceDatabase(base.InstanceRef)),
		nonUnknown(base.Instance),
	)
}

func nonUnknown(s string) string {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "", "unknown", "database", "db", "rdbms", "sql", "jdbc":
		return ""
	default:
		return s
	}
}

func stringDetail(d map[string]any, keys ...string) string {
	for _, k := range keys {
		if d == nil {
			continue
		}
		if v, ok := d[k]; ok {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" && s != "<nil>" && s != "[]" && s != "{}" {
				return s
			}
		}
	}
	return ""
}

func anyDetail(d map[string]any, keys ...string) any {
	for _, k := range keys {
		if d != nil {
			if v, ok := d[k]; ok {
				return v
			}
		}
	}
	return nil
}

func stringSliceDetail(d map[string]any, keys ...string) []string {
	v := anyDetail(d, keys...)
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, it := range t {
			if s := strings.TrimSpace(fmt.Sprint(it)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if strings.TrimSpace(t) == "" {
			return nil
		}
		return []string{t}
	default:
		return nil
	}
}

func inputsFromBase(base model.BaseEntity) *protocol.HTTPInputs {
	if len(base.Inputs) == 0 {
		return nil
	}
	out := &protocol.HTTPInputs{}
	for _, in := range base.Inputs {
		f := protocol.HTTPField{Name: in.Name, Type: in.Type, Required: in.Required}
		lower := strings.ToLower(in.Type + " " + in.Description)
		switch {
		case strings.Contains(lower, "path"):
			out.PathParams = append(out.PathParams, f)
		case strings.Contains(lower, "query"):
			out.QueryParams = append(out.QueryParams, f)
		case strings.Contains(lower, "header"):
			out.Headers = append(out.Headers, f)
		case strings.Contains(lower, "body"):
			out.Body = &protocol.BodySpec{
				ContentType: "application/json",
				Required:    in.Required,
			}
			if typ := strings.TrimSpace(in.Type); typ != "" {
				out.Body.Schema = map[string]any{"type": "object", "name": typ}
			}
		default:
			out.Metadata = ensureMap(out.Metadata)
			out.Metadata[in.Name] = map[string]any{"type": in.Type, "required": in.Required, "description": in.Description}
		}
	}
	return out
}

func ensureMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func responsesFromDetails(d map[string]any) []protocol.HTTPResponse {
	if raw, ok := d["responses"]; ok {
		out := responsesFromAny(raw)
		if len(out) > 0 {
			return out
		}
	}
	if s := stringDetail(d, "status", "response_status"); s != "" {
		var code int
		if _, err := fmt.Sscanf(s, "%d", &code); err == nil && code > 0 {
			return []protocol.HTTPResponse{{Status: code}}
		}
	}
	return nil
}

func responsesFromAny(raw any) []protocol.HTTPResponse {
	switch v := raw.(type) {
	case []map[string]any:
		out := make([]protocol.HTTPResponse, 0, len(v))
		for _, item := range v {
			if r, ok := responseFromMap(item); ok {
				out = append(out, r)
			}
		}
		return out
	case []any:
		out := make([]protocol.HTTPResponse, 0, len(v))
		for _, item := range v {
			switch m := item.(type) {
			case map[string]any:
				if r, ok := responseFromMap(m); ok {
					out = append(out, r)
				}
			case map[string]string:
				anyMap := map[string]any{}
				for k, val := range m {
					anyMap[k] = val
				}
				if r, ok := responseFromMap(anyMap); ok {
					out = append(out, r)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func responseFromMap(m map[string]any) (protocol.HTTPResponse, bool) {
	var code int
	switch v := m["status"].(type) {
	case int:
		code = v
	case int64:
		code = int(v)
	case float64:
		code = int(v)
	case string:
		if _, err := fmt.Sscanf(v, "%d", &code); err != nil {
			code = 0
		}
	}
	if code <= 0 {
		return protocol.HTTPResponse{}, false
	}
	resp := protocol.HTTPResponse{
		Status:      code,
		Error:       strings.TrimSpace(fmt.Sprint(m["error"])),
		ContentType: strings.TrimSpace(fmt.Sprint(m["content_type"])),
	}
	if resp.Error == "<nil>" {
		resp.Error = ""
	}
	if resp.ContentType == "<nil>" {
		resp.ContentType = ""
	}
	switch schema := m["schema"].(type) {
	case map[string]any:
		resp.Schema = schema
	case map[string]string:
		resp.Schema = map[string]any{}
		for k, v := range schema {
			resp.Schema[k] = v
		}
	case string:
		if strings.TrimSpace(schema) != "" {
			resp.Schema = map[string]any{"type": "object", "name": strings.TrimSpace(schema)}
		}
	}
	return resp, true
}

func authFromDetails(d map[string]any) *protocol.Auth {
	auth := strings.ToLower(stringDetail(d, "auth", "authentication"))
	if auth == "" {
		return nil
	}
	return &protocol.Auth{Required: auth != "none" && auth != "none visible", Schemes: nonEmptyStrings(auth)}
}

func ormFromDetails(d map[string]any) *protocol.ORMSpec {
	lib := stringDetail(d, "orm", "library", "framework")
	modelName := stringDetail(d, "model", "entity")
	method := stringDetail(d, "repository_method", "method", "operation")
	if lib == "" && modelName == "" && method == "" {
		return nil
	}
	return &protocol.ORMSpec{Library: lib, Model: modelName, Method: method}
}

func condition(c model.Condition) *protocol.Condition {
	if c.Kind == "" && c.Expression == "" && c.Explanation == "" {
		return nil
	}
	return &protocol.Condition{
		Summary:    firstNonEmpty(c.Explanation, c.Kind),
		Expression: c.Expression,
		Confidence: protocol.ConfidenceMedium,
	}
}

func reachability(c model.Condition) protocol.Reachability {
	if strings.TrimSpace(c.Expression) == "" || strings.EqualFold(c.Expression, "true") || strings.EqualFold(c.Kind, "unconditional") {
		return protocol.ReachabilityMust
	}
	return protocol.ReachabilityConditional
}

func confidence(v float64) protocol.Confidence {
	switch {
	case v >= 0.85:
		return protocol.ConfidenceHigh
	case v >= 0.65:
		return protocol.ConfidenceMedium
	case v > 0:
		return protocol.ConfidenceLow
	default:
		return protocol.ConfidenceUnknown
	}
}

func targetRef(name string) *protocol.TargetRef {
	return targetRefWithType(name, "")
}

func targetRefWithType(name, targetType string) *protocol.TargetRef {
	name = strings.TrimSpace(name)
	if name == "" || looksLikeHTTPOperationLabel(name) || strings.HasPrefix(name, "/") || looksLikeHTTPMethodSlug(name) {
		return &protocol.TargetRef{Type: "unresolved", Unresolved: true}
	}
	typ := strings.ToLower(strings.TrimSpace(targetType))
	if typ != "external" && typ != "service" {
		typ = "service"
	}
	prefix := typ + "."
	if typ == "service" {
		prefix = "service."
	}
	return &protocol.TargetRef{Type: typ, Ref: prefix + serviceRefSlug(name), Unresolved: false}
}

func serviceRefSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "service.")
	s = strings.TrimPrefix(s, "external.")
	s = strings.ReplaceAll(s, "_", "-")
	s = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "unknown"
	}
	return s
}

func looksLikeHTTPOperationLabel(raw string) bool {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) < 2 {
		return false
	}
	switch strings.ToUpper(fields[0]) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return strings.HasPrefix(fields[1], "/")
	default:
		return false
	}
}

func looksLikeHTTPMethodSlug(raw string) bool {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "service.")
	raw = strings.ReplaceAll(strings.ToLower(raw), "_", "-")
	for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options"} {
		if raw == method || strings.HasPrefix(raw, method+"-") {
			return true
		}
	}
	return false
}

func targetFromInstance(ref *model.InstanceRef) string {
	if ref == nil {
		return ""
	}
	return firstNonEmpty(ref.LogicalName, ref.Host, hostFromURL(ref.ResolvedURL), hostFromURL(ref.URLTemplate))
}

func instanceDatabase(ref *model.InstanceRef) string {
	if ref == nil {
		return ""
	}
	return ref.Database
}

func fingerprint(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("sha256:%x", sum[:])
}

func hostFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Hostname()
}

func providerFromRemote(remote string) string {
	lower := strings.ToLower(remote)
	if strings.Contains(lower, "github") {
		return "github"
	}
	if strings.TrimSpace(remote) != "" {
		return "git"
	}
	return ""
}

func detectedServiceName(repoPath string, facts *extraction.RepoFacts) string {
	if facts != nil {
		return facts.ServiceName
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" && s != "<nil>" {
			return s
		}
	}
	return ""
}

func nonEmptyStrings(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func appendUniqueStrings(in []string, vals ...string) []string {
	seen := map[string]struct{}{}
	out := append([]string{}, in...)
	for _, v := range out {
		seen[v] = struct{}{}
	}
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func semanticIDPart(s string) string {
	return slug(s)
}

var nonID = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "{", "")
	s = strings.ReplaceAll(s, "}", "")
	s = strings.ReplaceAll(s, "$", "")
	s = nonID.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "unknown"
	}
	return s
}

func isHTTPMethod(s string) bool {
	switch strings.ToUpper(s) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "ANY":
		return true
	default:
		return false
	}
}

func normalizeDBOperation(op string) string {
	op = strings.ToLower(strings.TrimSpace(op))
	switch {
	case strings.Contains(op, "insert"), strings.Contains(op, "create"), strings.Contains(op, "write"), strings.Contains(op, "save"):
		return "create"
	case strings.Contains(op, "update"):
		return "update"
	case strings.Contains(op, "delete"), strings.Contains(op, "remove"):
		return "delete"
	case strings.Contains(op, "upsert"), strings.Contains(op, "merge"):
		return "upsert"
	case strings.Contains(op, "read"), strings.Contains(op, "select"), strings.Contains(op, "find"), strings.Contains(op, "get"):
		return "read"
	case op != "":
		return op
	default:
		return "unknown"
	}
}

func accessForOperation(op string) string {
	switch normalizeDBOperation(op) {
	case "read":
		return "read"
	case "delete":
		return "delete"
	case "create", "update", "upsert":
		return "write"
	default:
		return "unknown"
	}
}

func flowKind(fromType, toType string) string {
	switch {
	case strings.Contains(fromType, "http") && strings.Contains(toType, "db"):
		return "api_to_database"
	case strings.Contains(fromType, "http") && strings.Contains(toType, "http"):
		return "api_to_http"
	case strings.Contains(toType, "queue"):
		return "publish_flow"
	default:
		return "request_flow"
	}
}

func perspectiveFor(t string, idx int) string {
	if idx > 0 {
		return "service_logic"
	}
	switch t {
	case "http_route", "webhook":
		return "route_registration"
	case "db_operation":
		return "repository_method"
	case "queue_consumer", "queue_publish":
		return "command_handler"
	case "cli_command":
		return "command_registration"
	case "scheduled_job":
		return "kubernetes_cronjob"
	default:
		return "unknown"
	}
}

func detectorName(base model.BaseEntity, fallback string) string {
	if base.PluginSource != "" {
		return "diffmind." + base.PluginSource
	}
	for _, tag := range base.Tags {
		if strings.HasPrefix(tag, "framework:") {
			return "diffmind." + strings.TrimPrefix(tag, "framework:")
		}
	}
	return "diffmind." + fallback
}

func sourceForEvidence(source string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	switch {
	case strings.Contains(source, "config"):
		return "config"
	case strings.Contains(source, "infra"), strings.Contains(source, "helm"), strings.Contains(source, "kubernetes"):
		return "infra"
	case strings.Contains(source, "human"):
		return "human"
	default:
		return "code"
	}
}

func symbolFromDetails(d map[string]any) string {
	return stringDetail(d, "handler", "entry_method", "service_method", "repository_method", "method_path", "repo_class_or_method")
}

func hasTag(tags []string, needle string) bool {
	for _, t := range tags {
		if strings.EqualFold(strings.TrimSpace(t), needle) {
			return true
		}
	}
	return false
}
