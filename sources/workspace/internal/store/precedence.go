package store

// Effective-configuration resolution. Repo-level overrides win over
// project-level fallbacks; this is the single source of truth the run manager
// and tests rely on.

// EffectiveInstruction returns the extraction instruction to use for a repo:
// the repo's own instruction when set, otherwise the project's.
func EffectiveInstruction(p *Project, r *Repo) string {
	if r != nil && r.Instruction != "" {
		return r.Instruction
	}
	if p != nil {
		return p.Instruction
	}
	return ""
}

// EffectiveBlueprintIDs returns the blueprint ids that apply to a repo: the
// repo's explicit override list when non-empty, otherwise nil to signal
// "fall back to project-level blueprint matching" (all project blueprints are
// candidates, matched by their applies_to rules).
func EffectiveBlueprintIDs(p *Project, r *Repo) []string {
	if r != nil && len(r.BlueprintIDs) > 0 {
		return append([]string(nil), r.BlueprintIDs...)
	}
	return nil
}
