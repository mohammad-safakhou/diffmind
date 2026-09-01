package knowledge

// BuiltInPacks returns the small set of conventions shipped with every
// DiffMind binary. Project or installed packs with the same ID replace the
// built-in copy.
func BuiltInPacks() []*Pack {
	return []*Pack{{
		APIVersion:    APIVersion,
		Kind:          Kind,
		ID:            "helm-values",
		Name:          "Helm values service identity",
		Description:   "Extracts service identity, ingress names, and owned queues from a conventional Helm values file.",
		Version:       "1.0.0",
		License:       "Apache-2.0",
		Compatibility: ">=0.1.0",
		Priority:      -1000,
		AppliesTo:     AppliesTo{Kind: "service_repo", Match: MatchConfig{HasFile: "deploy/values.yaml"}},
		Ignore:        []string{"**/vendor/**"},
		Extractions: []Extraction{{
			Name:     "service identity",
			Source:   ExtractionSource{Glob: "deploy/values.yaml"},
			Strategy: "field_path",
			Extract: []ExtractField{
				{Field: "service.name", MapsTo: "service_name"},
				{Field: "ingress.hosts", MapsTo: "dns_aliases"},
				{Field: "service.port", MapsTo: "metadata.container_port"},
				{Field: "queues.owned", MapsTo: "queue_identifiers"},
			},
		}},
	}}
}

func WithBuiltIns(packs []*Pack) []*Pack {
	ids := make(map[string]bool, len(packs))
	for _, pack := range packs {
		ids[pack.ID] = true
	}
	out := append([]*Pack(nil), packs...)
	for _, pack := range BuiltInPacks() {
		if !ids[pack.ID] {
			out = append(out, pack)
		}
	}
	return out
}
