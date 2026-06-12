package eval

import (
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// Mode selects which expected items are in scope.
type Mode int

const (
	// ModeCheap scores only deterministic:true expected items against the
	// deterministic floor (no LLM). It measures "floor recall vs. what the
	// floor is supposed to find", so it is hermetic and CI-able.
	ModeCheap Mode = iota
	// ModeFull scores ALL expected items against a real pipeline run.
	ModeFull
)

func (m Mode) String() string {
	if m == ModeFull {
		return "full"
	}
	return "cheap"
}

// Extracted is the extractor output to score (a subset of agents.Result / the
// run artifacts: only the fields the scorer needs).
type Extracted struct {
	Exposures    []model.Exposure
	Dependencies []model.Dependency
	Connections  []model.Connection
}

// ItemRef identifies a false positive / false negative so a human can debug it.
type ItemRef struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Key  string `json:"key"`
}

// InstanceMismatch is a MATCHED fact whose concrete instance differs from the
// label. Identity matching deliberately ignores instance (an http route is its
// method+path whatever broker config surrounds it); this is the separate
// downstream-contract check: two services referencing the same physical
// queue/database must emit the same instance, or the cross-service graph
// splits one node into many.
type InstanceMismatch struct {
	Type string `json:"type"`
	Key  string `json:"key"`
	Want string `json:"want"`
	Got  string `json:"got"`
}

// ObjectiveScore is the score for one objective (entity type) or the
// "connections" pseudo-objective, or the micro-averaged overall.
type ObjectiveScore struct {
	Objective          string             `json:"objective"`
	TP                 int                `json:"tp"`
	FP                 int                `json:"fp"`
	FN                 int                `json:"fn"`
	Precision          float64            `json:"precision"`
	Recall             float64            `json:"recall"`
	F1                 float64            `json:"f1"`
	FalsePositives     []ItemRef          `json:"false_positives,omitempty"`
	FalseNegatives     []ItemRef          `json:"false_negatives,omitempty"`
	InstanceMismatches []InstanceMismatch `json:"instance_mismatches,omitempty"`
}

// Report is the full scoring result for one fixture+mode.
type Report struct {
	Fixture    string           `json:"fixture"`
	Mode       string           `json:"mode"`
	Objectives []ObjectiveScore `json:"objectives"`
	Overall    ObjectiveScore   `json:"overall"`
}

type keyed struct {
	key      string
	ref      ItemRef
	instance string // lowercased Instance, for the downstream-contract check
}

// matchByKey computes TP/FP/FN by multiset key match. Two items with the same
// key are the same fact; surplus on the extracted side is a false positive,
// surplus on the expected side a false negative. Order-stable for debuggable
// FP/FN lists.
func matchByKey(extracted, expected []keyed) (tp int, fps, fns []ItemRef) {
	extCount := map[string]int{}
	for _, x := range extracted {
		extCount[x.key]++
	}
	expCount := map[string]int{}
	for _, e := range expected {
		expCount[e.key]++
	}
	matched := map[string]int{}
	for k, ec := range extCount {
		m := ec
		if expCount[k] < m {
			m = expCount[k]
		}
		if m > 0 {
			matched[k] = m
			tp += m
		}
	}
	seenExt := map[string]int{}
	for _, x := range extracted {
		if seenExt[x.key] < matched[x.key] {
			seenExt[x.key]++
			continue
		}
		fps = append(fps, x.ref)
	}
	seenExp := map[string]int{}
	for _, e := range expected {
		if seenExp[e.key] < matched[e.key] {
			seenExp[e.key]++
			continue
		}
		fns = append(fns, e.ref)
	}
	return tp, fps, fns
}

func prf(tp, fp, fn int) (p, r, f1 float64) {
	if tp+fp == 0 {
		p = 1
	} else {
		p = float64(tp) / float64(tp+fp)
	}
	if tp+fn == 0 {
		r = 1
	} else {
		r = float64(tp) / float64(tp+fn)
	}
	if p+r == 0 {
		f1 = 0
	} else {
		f1 = 2 * p * r / (p + r)
	}
	return
}

// ScoreAll scores extracted against expected. In ModeCheap only deterministic
// expected items are in scope.
func ScoreAll(extracted Extracted, expected ExpectedSet, mode Mode) Report {
	rep := Report{Fixture: expected.Fixture, Mode: mode.String()}

	// Bucket extracted entities by type.
	extByType := map[string][]keyed{}
	for _, e := range extracted.Exposures {
		extByType[e.Type] = append(extByType[e.Type], keyedEntity(e.BaseEntity))
	}
	for _, d := range extracted.Dependencies {
		extByType[d.Type] = append(extByType[d.Type], keyedEntity(d.BaseEntity))
	}

	// Bucket expected entities by type (filtered by mode).
	expByType := map[string][]keyed{}
	addExp := func(e ExpectedEntity) {
		if mode == ModeCheap && !e.Deterministic {
			return
		}
		expByType[e.Type] = append(expByType[e.Type], keyedEntity(e.toBase()))
	}
	for _, e := range expected.Exposures {
		addExp(e)
	}
	for _, e := range expected.Dependencies {
		addExp(e)
	}

	// Score every type present on either side.
	types := map[string]struct{}{}
	for t := range extByType {
		types[t] = struct{}{}
	}
	for t := range expByType {
		types[t] = struct{}{}
	}
	typeList := make([]string, 0, len(types))
	for t := range types {
		typeList = append(typeList, t)
	}
	sort.Strings(typeList)

	var micro ObjectiveScore
	micro.Objective = "overall"
	for _, t := range typeList {
		tp, fps, fns := matchByKey(extByType[t], expByType[t])
		os := buildScore(t, tp, fps, fns)
		os.InstanceMismatches = instanceMismatches(extByType[t], expByType[t])
		rep.Objectives = append(rep.Objectives, os)
		micro.TP += os.TP
		micro.FP += os.FP
		micro.FN += os.FN
	}

	// Connections: translate this run's hash IDs to identity keys, then match
	// by endpoint-identity pairs.
	idToKey := map[string]string{}
	for _, e := range extracted.Exposures {
		idToKey[e.ID] = identityKey(e.BaseEntity)
	}
	for _, d := range extracted.Dependencies {
		idToKey[d.ID] = identityKey(d.BaseEntity)
	}
	var extConns, expConns []keyed
	for _, c := range extracted.Connections {
		key := connectionPairKey(idToKey[c.FromExposureID], idToKey[c.ToDependencyID])
		extConns = append(extConns, keyed{key: key, ref: ItemRef{Type: "connection", Name: c.Summary, Key: key}})
	}
	for _, c := range expected.Connections {
		if mode == ModeCheap && !c.Deterministic {
			continue
		}
		key := connectionPairKey(identityKey(c.From.toBase()), identityKey(c.To.toBase()))
		expConns = append(expConns, keyed{key: key, ref: ItemRef{Type: "connection", Name: key, Key: key}})
	}
	if len(extConns) > 0 || len(expConns) > 0 {
		tp, fps, fns := matchByKey(extConns, expConns)
		os := buildScore("connections", tp, fps, fns)
		rep.Objectives = append(rep.Objectives, os)
		micro.TP += os.TP
		micro.FP += os.FP
		micro.FN += os.FN
	}

	micro.Precision, micro.Recall, micro.F1 = prf(micro.TP, micro.FP, micro.FN)
	rep.Overall = micro
	return rep
}

func keyedEntity(b model.BaseEntity) keyed {
	k := identityKey(b)
	return keyed{key: k, ref: ItemRef{Type: b.Type, Name: b.Name, Key: k}, instance: lc(b.Instance)}
}

// instanceMismatches checks the matched pairs whose label pins an instance: at
// least one extracted entity under the same identity key must carry it.
// Unmatched keys are already FN/FP; this only grades identity-correct facts
// with the wrong concrete instance.
func instanceMismatches(extracted, expected []keyed) []InstanceMismatch {
	var out []InstanceMismatch
	reported := map[string]struct{}{}
	for _, e := range expected {
		if e.instance == "" {
			continue
		}
		if _, done := reported[e.key]; done {
			continue
		}
		var got []string
		matched, instanceOK := false, false
		for _, x := range extracted {
			if x.key != e.key {
				continue
			}
			matched = true
			got = append(got, x.instance)
			if x.instance == e.instance {
				instanceOK = true
			}
		}
		if !matched || instanceOK {
			continue
		}
		reported[e.key] = struct{}{}
		out = append(out, InstanceMismatch{
			Type: e.ref.Type,
			Key:  e.key,
			Want: e.instance,
			Got:  strings.Join(got, ", "),
		})
	}
	return out
}

func buildScore(objective string, tp int, fps, fns []ItemRef) ObjectiveScore {
	p, r, f1 := prf(tp, len(fps), len(fns))
	return ObjectiveScore{
		Objective:      objective,
		TP:             tp,
		FP:             len(fps),
		FN:             len(fns),
		Precision:      p,
		Recall:         r,
		F1:             f1,
		FalsePositives: fps,
		FalseNegatives: fns,
	}
}
