package graphschema

import (
	"errors"
	"fmt"
	"strings"
)

// ValidateGraph enforces core graph schema invariants so persisted artifacts are query-safe.
func ValidateGraph(graph Graph) error {
	if strings.TrimSpace(graph.GraphID) == "" {
		return errors.New("graph_id is required")
	}
	if graph.GeneratedAt.IsZero() {
		return errors.New("generated_at is required")
	}
	if strings.TrimSpace(graph.Mode) == "" {
		return errors.New("mode is required")
	}
	if strings.TrimSpace(graph.Meta.TenantID) == "" {
		return errors.New("meta.tenant_id is required")
	}

	nodeByID := make(map[string]Node, len(graph.Nodes))
	for i, n := range graph.Nodes {
		if err := validateNode(n); err != nil {
			return fmt.Errorf("nodes[%d] invalid: %w", i, err)
		}
		if _, exists := nodeByID[n.ID]; exists {
			return fmt.Errorf("duplicate node id %q", n.ID)
		}
		nodeByID[n.ID] = n
	}

	edgeByID := make(map[string]struct{}, len(graph.Edges))
	for i, e := range graph.Edges {
		if err := validateEdge(e); err != nil {
			return fmt.Errorf("edges[%d] invalid: %w", i, err)
		}
		if _, exists := edgeByID[e.ID]; exists {
			return fmt.Errorf("duplicate edge id %q", e.ID)
		}
		edgeByID[e.ID] = struct{}{}
		if _, ok := nodeByID[e.SourceID]; !ok {
			return fmt.Errorf("edge %q source_id %q not found in nodes", e.ID, e.SourceID)
		}
		if _, ok := nodeByID[e.TargetID]; !ok {
			return fmt.Errorf("edge %q target_id %q not found in nodes", e.ID, e.TargetID)
		}
	}

	if graph.Stats.NodeCount != len(graph.Nodes) {
		return fmt.Errorf("stats.node_count=%d does not match nodes=%d", graph.Stats.NodeCount, len(graph.Nodes))
	}
	if graph.Stats.EdgeCount != len(graph.Edges) {
		return fmt.Errorf("stats.edge_count=%d does not match edges=%d", graph.Stats.EdgeCount, len(graph.Edges))
	}
	if err := validateTypeCounts(graph); err != nil {
		return err
	}
	return nil
}

func validateNode(n Node) error {
	if strings.TrimSpace(n.ID) == "" {
		return errors.New("id is required")
	}
	if strings.TrimSpace(n.Type) == "" {
		return errors.New("type is required")
	}
	if strings.TrimSpace(n.Label) == "" {
		return errors.New("label is required")
	}
	if n.Confidence < 0 || n.Confidence > 1 {
		return errors.New("confidence must be in [0,1]")
	}
	return nil
}

func validateEdge(e Edge) error {
	if strings.TrimSpace(e.ID) == "" {
		return errors.New("id is required")
	}
	if strings.TrimSpace(e.Type) == "" {
		return errors.New("type is required")
	}
	if strings.TrimSpace(e.SourceID) == "" {
		return errors.New("source_id is required")
	}
	if strings.TrimSpace(e.TargetID) == "" {
		return errors.New("target_id is required")
	}
	if e.Confidence < 0 || e.Confidence > 1 {
		return errors.New("confidence must be in [0,1]")
	}
	for i, ref := range e.EvidenceRefs {
		if err := validateEvidenceRef(ref); err != nil {
			return fmt.Errorf("evidence_refs[%d] invalid: %w", i, err)
		}
	}
	return nil
}

func validateEvidenceRef(ref EvidenceRef) error {
	// Allow sparse refs, but require at least one locator field.
	if strings.TrimSpace(ref.SnapshotID) == "" &&
		strings.TrimSpace(ref.FilePath) == "" &&
		strings.TrimSpace(ref.FactID) == "" &&
		strings.TrimSpace(ref.EvidenceID) == "" {
		return errors.New("at least one of snapshot_id, file_path, fact_id, evidence_id is required")
	}
	return nil
}

func validateTypeCounts(graph Graph) error {
	expectedNodeByType := map[string]int{}
	for _, n := range graph.Nodes {
		expectedNodeByType[n.Type]++
	}
	for typ, expected := range expectedNodeByType {
		if graph.Stats.ByNode[typ] != expected {
			return fmt.Errorf("stats.by_node[%q]=%d does not match expected=%d", typ, graph.Stats.ByNode[typ], expected)
		}
	}
	for typ, got := range graph.Stats.ByNode {
		if expectedNodeByType[typ] != got {
			return fmt.Errorf("stats.by_node has unexpected/mismatched type %q=%d", typ, got)
		}
	}

	expectedEdgeByType := map[string]int{}
	for _, e := range graph.Edges {
		expectedEdgeByType[e.Type]++
	}
	for typ, expected := range expectedEdgeByType {
		if graph.Stats.ByEdge[typ] != expected {
			return fmt.Errorf("stats.by_edge[%q]=%d does not match expected=%d", typ, graph.Stats.ByEdge[typ], expected)
		}
	}
	for typ, got := range graph.Stats.ByEdge {
		if expectedEdgeByType[typ] != got {
			return fmt.Errorf("stats.by_edge has unexpected/mismatched type %q=%d", typ, got)
		}
	}
	return nil
}
