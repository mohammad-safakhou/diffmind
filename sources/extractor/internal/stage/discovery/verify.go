package discovery

import (
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

// verify.go holds the pure merge + helpers for the optional Stage-1.5 discovery
// verification pass. The runner owns the LLM calls and events; this file owns
// the KEEP-biased reconciliation so it is table-testable without a server.
//
// Stage isolation (CLAUDE.md invariant): we deliberately re-implement the tiny
// retain-on-doubt helpers instead of importing reexamine — stages never import
// one another. The semantics mirror reexamine's C3 policy (a single doubt must
// not delete an evidence-backed finding).

// verifyDoubtedTag marks an item the verification pass did not re-confirm but
// which was retained (downgraded) anyway. Mirrors reexamine's
// "reexamination_doubted" so downstream consumers can tell why confidence fell.
const verifyDoubtedTag = "discovery_verify_doubted"

// MergeVerify folds a verification pass's returned items back into the originals,
// KEEP-biased:
//   - An original CONFIRMED by the pass (same semantic key) is kept, taking the
//     higher confidence and the union of locations/evidence.
//   - An original NOT confirmed is RETAINED — downgraded and tagged — unless it
//     is structurally unverifiable (no name/type/location), the only case where
//     an unconfirmed item is dropped.
//   - An item the pass NEWLY found (not among originals) is added only when it is
//     evidence-backed (has a real source location), so the pass can raise recall
//     without inventing locationless entities.
//
// Order is stable: originals first (in their original order), then new finds.
func MergeVerify(obj objectives.Objective, originals, verified []extraction.Candidate, minConf float64) []extraction.Candidate {
	verifiedByKey := make(map[string]extraction.Candidate, len(verified))
	for _, v := range verified {
		verifiedByKey[extraction.DiscoverySemanticKey(obj, v)] = v
	}

	seen := map[string]bool{}
	out := make([]extraction.Candidate, 0, len(originals)+len(verified))

	for _, o := range originals {
		k := extraction.DiscoverySemanticKey(obj, o)
		seen[k] = true
		if v, ok := verifiedByKey[k]; ok {
			// Confirmed (possibly corrected): take the higher-confidence base
			// and union the corroborating evidence/locations.
			merged := o
			if v.Confidence > o.Confidence {
				merged = v
			}
			merged.Locations = UnionLocations(o.Locations, v.Locations)
			merged.Evidence = append(append([]extraction.Evidence(nil), o.Evidence...), v.Evidence...)
			out = append(out, merged)
			continue
		}
		// Not re-confirmed. Drop ONLY if it could never be verified anyway;
		// otherwise retain it downgraded so a single doubt can't delete real
		// architecture.
		if verifyStructurallyUnverifiable(o) {
			continue
		}
		d := o
		d.Confidence = verifyDowngrade(o.Confidence, minConf)
		d.Tags = appendUniqueTag(d.Tags, verifyDoubtedTag)
		out = append(out, d)
	}

	// Items the pass found that discovery missed. Add only evidence-backed ones.
	for _, v := range verified {
		k := extraction.DiscoverySemanticKey(obj, v)
		if seen[k] {
			continue
		}
		seen[k] = true
		if verifyStructurallyUnverifiable(v) {
			continue
		}
		out = append(out, v)
	}
	return out
}

// verifyStructurallyUnverifiable reports whether a candidate lacks the minimum
// to be a real, locatable fact (no name, no type, or no source file). Such an
// item may be dropped on a single non-confirmation; everything else is retained.
func verifyStructurallyUnverifiable(c extraction.Candidate) bool {
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Type) == "" {
		return true
	}
	for _, l := range c.Locations {
		if strings.TrimSpace(l.File) != "" {
			return false
		}
	}
	return true
}

// verifyDowngrade lowers a doubted item's confidence while keeping it at or above
// the run's MinConfidence floor, so a retained item still survives the later
// confidence gate (ToBase) instead of being silently dropped after we chose to
// keep it.
func verifyDowngrade(conf, minConf float64) float64 {
	lowered := conf * 0.7
	if lowered < minConf {
		return minConf
	}
	return lowered
}

func appendUniqueTag(tags []string, tag string) []string {
	for _, t := range tags {
		if t == tag {
			return tags
		}
	}
	return append(tags, tag)
}
