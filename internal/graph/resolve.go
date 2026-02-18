package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

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
				ID:       serviceToEndpointID,
				Type:     "service_calls_endpoint",
				SourceID: srcServiceNode,
				TargetID: endpointID,
				Attributes: map[string]any{
					"method":                 method,
					"target":                 target,
					"target_path_normalized": normalizePath(target),
				},
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
					g.addEdge(graphschema.Edge{
						ID:       edgeID("queue_delivers_to_service", queueID, src, target+"|"+method),
						Type:     "queue_delivers_to_service",
						SourceID: queueID,
						TargetID: src,
						Attributes: map[string]any{
							"topic":         target,
							"source":        "analyzer",
							"library":       e.Attributes["library"],
							"canonical_key": canonical,
						},
						Confidence:   e.Confidence,
						Inferred:     false,
						EvidenceRefs: evidence,
					})
				} else {
					g.addEdge(graphschema.Edge{
						ID:       edgeID("service_publishes_queue", src, queueID, target+"|"+method),
						Type:     "service_publishes_queue",
						SourceID: src,
						TargetID: queueID,
						Attributes: map[string]any{
							"topic":         target,
							"source":        "analyzer",
							"library":       e.Attributes["library"],
							"canonical_key": canonical,
						},
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
					g.addEdge(graphschema.Edge{
						ID:       edgeID("service_reads_db", src, dbID, target+"|"+method),
						Type:     "service_reads_db",
						SourceID: src,
						TargetID: dbID,
						Attributes: map[string]any{
							"database":      target,
							"source":        "analyzer",
							"library":       e.Attributes["library"],
							"canonical_key": canonical,
						},
						Confidence:   e.Confidence,
						Inferred:     false,
						EvidenceRefs: evidence,
					})
				} else {
					g.addEdge(graphschema.Edge{
						ID:       edgeID("service_writes_db", src, dbID, target+"|"+method),
						Type:     "service_writes_db",
						SourceID: src,
						TargetID: dbID,
						Attributes: map[string]any{
							"database":      target,
							"source":        "analyzer",
							"library":       e.Attributes["library"],
							"canonical_key": canonical,
						},
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
		canonNodeID := canonicalServiceNodeID(key)
		g.addNode(graphschema.Node{
			ID:         canonNodeID,
			Type:       "canonical_service",
			Label:      key,
			Attributes: map[string]any{"canonical_key": key, "member_count": len(unique)},
			Confidence: 0.85,
			Inferred:   true,
		})
		for _, svc := range unique {
			svcNodeID := serviceNodeID(svc.spec.ID)
			if _, ok := seenService[svcNodeID+"|"+canonNodeID]; ok {
				continue
			}
			seenService[svcNodeID+"|"+canonNodeID] = struct{}{}
			g.addEdge(graphschema.Edge{
				ID:         edgeID("service_alias_of_canonical_service", svcNodeID, canonNodeID, key),
				Type:       "service_alias_of_canonical_service",
				SourceID:   svcNodeID,
				TargetID:   canonNodeID,
				Attributes: map[string]any{"canonical_key": key},
				Confidence: 0.85,
				Inferred:   true,
			})
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

type bundleioEntityRef struct {
	id          string
	attributes  map[string]any
	confidence  float64
	factIDs     []string
	evidenceIDs []string
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

func canonicalQueueNodeID(key string) string {
	return "canon:queue:" + stableID(strings.ToLower(strings.TrimSpace(key)))
}

func canonicalDatabaseNodeID(key string) string {
	return "canon:db:" + stableID(strings.ToLower(strings.TrimSpace(key)))
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
	default:
		return ""
	}
}
