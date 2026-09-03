// Package packtest tests pack contributions against the production resolver and
// the same rich graph representation used by the UI and MCP.
package packtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/archgraph"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/knowledge"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/model"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/registry"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/resolver"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
)

func RunTests(pack *knowledge.Pack) []knowledge.TestResult {
	results := knowledge.RunTests(pack)
	for _, test := range pack.GraphTests {
		result := knowledge.TestResult{Name: test.Name}
		if err := runGraphTest(pack, test); err != nil {
			result.Error = err.Error()
		} else {
			result.Passed = true
		}
		results = append(results, result)
	}
	return results
}

func runGraphTest(pack *knowledge.Pack, test knowledge.GraphTest) error {
	log := util.NewLogger(util.LevelInfo)
	reg := registry.New()
	supplements := map[string]archgraph.Supplement{}
	// Empty artifact directories guarantee fixture tests cannot accidentally
	// consume a developer's previous .diffmind runs or execute source code.
	root, err := os.MkdirTemp("", "diffmind-pack-graph-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	dirs := map[string]string{}
	for _, repo := range test.Repositories {
		fixture, err := knowledge.FixturePath(pack, repo.Fixture)
		if err != nil {
			return err
		}
		dirs[repo.Name] = filepath.Join(root, repo.Name)
		if err := os.MkdirAll(dirs[repo.Name], 0o755); err != nil {
			return err
		}
		var detected knowledge.DetectionResult
		identity := model.ServiceIdentity{ServiceName: repo.Name, RepoPath: fixture}
		if knowledge.Matches(pack, fixture, "service_repo") {
			detected, err = knowledge.Detect(context.Background(), pack, fixture, repo.Name)
			if err != nil {
				return err
			}
			identity, err = knowledge.ToIdentity(repo.Name, fixture, knowledge.NewEngine(log).Run(pack, fixture))
			if err != nil {
				return err
			}
		}
		override, err := knowledge.LoadServiceOverride(fixture)
		if err != nil {
			return err
		}
		knowledge.ApplyServiceOverride(&identity, override)
		reg.AddArchitecture(repo.Name, &model.ServiceArchitecture{RepoPath: fixture, Dependencies: detected.Dependencies, Exposures: detected.Exposures})
		reg.AddIdentity(repo.Name, &identity)
		supplements[repo.Name] = archgraph.Supplement{Dependencies: detected.Dependencies, Exposures: detected.Exposures, Targets: map[string]archgraph.ResolvedTarget{}}
	}
	resolution, err := resolver.New(reg, log, knowledge.ResolutionRules([]*knowledge.Pack{pack})...).Resolve()
	if err != nil {
		return err
	}
	for _, match := range resolution.Matches {
		supplements[match.FromService].Targets[match.DependencyID] = archgraph.ResolvedTarget{Service: match.ToService, Reason: match.Reasoning, Confidence: match.Confidence}
	}
	graph := archgraph.BuildWithSupplements("pack-test", dirs, supplements)
	actual := make([]knowledge.ExpectedEdge, 0, len(graph.Edges))
	for _, edge := range graph.Edges {
		actual = append(actual, knowledge.ExpectedEdge{From: edge.From, To: edge.To, Type: edge.Type})
	}
	want := append([]knowledge.ExpectedEdge{}, test.Edges...)
	sortEdges(actual)
	sortEdges(want)
	if !reflect.DeepEqual(actual, want) {
		return fmt.Errorf("graph edges mismatch: want %+v, got %+v", want, actual)
	}
	return nil
}

func sortEdges(edges []knowledge.ExpectedEdge) {
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Type < b.Type
	})
}
