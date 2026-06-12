package connections

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// repair.go is the A1 connection-repair tail (Stage 4.5): exposures the
// deterministic walk left with ZERO connections get one evidence-gated LLM
// pass. Strictly additive — existing connections are never touched, and a
// failed repair degrades to the pre-repair result (fail-soft). The model picks
// targets from a closed set of existing dependency IDs; its cited evidence is
// validated against the file index, NOT by re-running the AST walk that
// already failed (that check would be circular).

// repairConfidenceCeiling caps repaired connections below the deterministic
// walk's shortest-path score, so provenance ranks honestly: ast > llm_repair.
const repairConfidenceCeiling = 0.85

type RepairPromptFunc func(ctx context.Context, jobID, prompt string, schema map[string]any) (map[string]any, error)

type RepairRunner struct {
	Prompt        RepairPromptFunc
	PathMapper    *extraction.PathMapper
	MinConfidence float64
}

type RepairInput struct {
	Index        *astpkg.ProjectIndex
	Exposures    []model.Exposure
	Dependencies []model.Dependency
	Connections  []model.Connection // post-walk; used to find dangling exposures
	RepoFacts    *extraction.RepoFacts
	SubDir       string
}

type RepairOutput struct {
	Connections []model.Connection // the ADDED connections only
	Rejected    []model.UnresolvedItem
	Dangling    int // exposures that had zero connections before repair
}

// Run finds zero-connection exposures and asks the model — in one batched call
// over the closed dependency set — which of them actually reach a known
// dependency. Returns only validated additions.
func (r RepairRunner) Run(ctx context.Context, in RepairInput) (RepairOutput, error) {
	dangling := danglingExposures(in.Exposures, in.Connections)
	out := RepairOutput{Dangling: len(dangling)}
	if len(dangling) == 0 || len(in.Dependencies) == 0 || r.Prompt == nil {
		return out, nil
	}

	prompt := extraction.BuildConnectionRepairPrompt(dangling, repairCandidates(in.Dependencies), in.RepoFacts, in.SubDir)
	payload, err := r.Prompt(ctx, "connections.repair", prompt, extraction.ConnectionRepairSchema())
	if err != nil {
		return out, err // caller degrades to the pre-repair result
	}

	proposals := parseRepairProposals(payload["connections"])
	expByID := indexExposures(dangling)
	depByID := indexDependencies(in.Dependencies)
	for _, p := range proposals {
		exp, expOK := expByID[p.FromExposureID]
		dep, depOK := depByID[p.ToDependencyID]
		reason := ""
		switch {
		case !expOK || !depOK:
			reason = "id not in the offered closed set"
		case p.Confidence < r.MinConfidence:
			reason = fmt.Sprintf("confidence %.2f below minimum %.2f", p.Confidence, r.MinConfidence)
		case len(validRepairEvidence(in.Index, r.PathMapper, p.Evidence)) == 0:
			reason = "no cited evidence resolves to an indexed file"
		}
		if reason != "" {
			out.Rejected = append(out.Rejected, model.UnresolvedItem{
				Kind:       model.KindExposure,
				Type:       "connection",
				Name:       p.FromExposureID + " -> " + p.ToDependencyID,
				ReasonCode: "llm_repair_rejected",
				Reason:     reason,
				Confidence: p.Confidence,
			})
			continue
		}
		out.Connections = append(out.Connections, r.buildRepairedConnection(in.Index, exp, dep, p))
	}
	sort.Slice(out.Connections, func(i, j int) bool { return out.Connections[i].ID < out.Connections[j].ID })
	return out, nil
}

type repairProposal struct {
	FromExposureID string               `json:"from_exposure_id"`
	ToDependencyID string               `json:"to_dependency_id"`
	Summary        string               `json:"summary"`
	Confidence     float64              `json:"confidence"`
	Evidence       []repairEvidenceItem `json:"evidence"`
}

type repairEvidenceItem struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Snippet   string `json:"snippet"`
}

func parseRepairProposals(raw any) []repairProposal {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var out []repairProposal
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

// validRepairEvidence keeps citations whose file (after snapshot→source path
// mapping) exists in the project index — source or config. Plausibility, not
// proof: the proof standard here is "the model had to read a real file", the
// circular alternative (re-confirm via the failed AST walk) is explicitly out.
func validRepairEvidence(idx *astpkg.ProjectIndex, pm *extraction.PathMapper, items []repairEvidenceItem) []repairEvidenceItem {
	var kept []repairEvidenceItem
	for _, e := range items {
		file := strings.TrimSpace(e.File)
		if pm != nil {
			file = pm.MapFile(file)
		}
		if file == "" || e.StartLine <= 0 || idx == nil {
			continue
		}
		_, isSource := idx.Files[file]
		_, isConfig := idx.Configs[file]
		if !isSource && !isConfig {
			continue
		}
		e.File = file
		if e.EndLine < e.StartLine {
			e.EndLine = e.StartLine
		}
		kept = append(kept, e)
	}
	return kept
}

func (r RepairRunner) buildRepairedConnection(idx *astpkg.ProjectIndex, exp model.Exposure, dep model.Dependency, p repairProposal) model.Connection {
	evidence := validRepairEvidence(idx, r.PathMapper, p.Evidence)
	locs := make([]model.Location, 0, len(evidence))
	mEvidence := make([]model.Evidence, 0, len(evidence))
	for _, e := range evidence {
		loc := model.Location{File: e.File, StartLine: e.StartLine, EndLine: e.EndLine}
		locs = append(locs, loc)
		mEvidence = append(mEvidence, model.Evidence{Location: loc, Snippet: e.Snippet, Source: model.ConnectionSourceLLMRepair})
	}
	confidence := p.Confidence
	if confidence > repairConfidenceCeiling {
		confidence = repairConfidenceCeiling
	}
	summary := strings.TrimSpace(p.Summary)
	if summary == "" {
		summary = fmt.Sprintf("%s → %s", exp.Name, dep.Name)
	}
	pathSig := "llm_repair:" + exp.ID + "->" + dep.ID
	return model.Connection{
		ID:             util.StableID(exp.ID, dep.ID, pathSig),
		FromExposureID: exp.ID,
		ToDependencyID: dep.ID,
		Source:         model.ConnectionSourceLLMRepair,
		Summary:        summary,
		Locations:      locs,
		Evidence:       mEvidence,
		Confidence:     confidence,
		FromType:       exp.Type,
		ToType:         dep.Type,
		PathSignature:  pathSig,
	}
}

func danglingExposures(exposures []model.Exposure, conns []model.Connection) []model.Exposure {
	connected := map[string]struct{}{}
	for _, c := range conns {
		connected[c.FromExposureID] = struct{}{}
	}
	var out []model.Exposure
	for _, e := range exposures {
		if _, ok := connected[e.ID]; !ok {
			out = append(out, e)
		}
	}
	return out
}

func repairCandidates(deps []model.Dependency) []extraction.RepairCandidate {
	out := make([]extraction.RepairCandidate, 0, len(deps))
	for _, d := range deps {
		c := extraction.RepairCandidate{ID: d.ID, Type: d.Type, Name: d.Name, Instance: d.Instance}
		if len(d.Locations) > 0 {
			c.Location = fmt.Sprintf("%s:%d", d.Locations[0].File, d.Locations[0].StartLine)
		}
		out = append(out, c)
	}
	return out
}

func indexExposures(in []model.Exposure) map[string]model.Exposure {
	out := make(map[string]model.Exposure, len(in))
	for _, e := range in {
		out[e.ID] = e
	}
	return out
}

func indexDependencies(in []model.Dependency) map[string]model.Dependency {
	out := make(map[string]model.Dependency, len(in))
	for _, d := range in {
		out[d.ID] = d
	}
	return out
}
