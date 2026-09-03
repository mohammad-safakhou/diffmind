package knowledge

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/model"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
)

type TestResult struct {
	Name       string                `json:"name"`
	Passed     bool                  `json:"passed"`
	Error      string                `json:"error,omitempty"`
	Actual     model.ServiceIdentity `json:"actual,omitempty"`
	Evidence   []ExtractionResult    `json:"evidence,omitempty"`
	Detections DetectionResult       `json:"detections,omitempty"`
}

// RunTests executes every fixture declared by a pack.
func RunTests(pack *Pack) []TestResult {
	results := make([]TestResult, 0, len(pack.Tests))
	engine := NewEngine(util.NewLogger(util.LevelInfo))
	for _, test := range pack.Tests {
		result := TestResult{Name: test.Name}
		fixture, err := FixturePath(pack, test.Fixture)
		if err != nil {
			result.Error = err.Error()
			results = append(results, result)
			continue
		}
		kind := test.RepoKind
		if kind == "" {
			kind = "service_repo"
		}
		if !Matches(pack, fixture, kind) {
			result.Error = fmt.Sprintf("fixture %s does not match pack applies_to rules", test.Fixture)
			results = append(results, result)
			continue
		}
		result.Evidence = engine.Run(pack, fixture)
		actual, err := ToIdentity(test.Expected.ServiceName, fixture, result.Evidence)
		if err != nil {
			result.Error = err.Error()
			results = append(results, result)
			continue
		}
		result.Actual = actual
		expected := model.ServiceIdentity{
			ServiceName: test.Expected.ServiceName,
			Aliases:     append([]model.IdentityAlias(nil), test.Expected.Aliases...),
			Resources:   append([]model.OwnedResource(nil), test.Expected.Resources...),
			Metadata:    test.Expected.Metadata,
		}
		actual.RepoPath = ""
		normalizeIdentity(&actual)
		normalizeIdentity(&expected)
		if !reflect.DeepEqual(actual, expected) {
			result.Error = fmt.Sprintf("identity mismatch: expected %+v, got %+v", expected, actual)
		} else {
			result.Detections, err = Detect(context.Background(), pack, fixture, actual.ServiceName)
			if err != nil {
				result.Error = err.Error()
			} else {
				var deps, exps []ExpectedDetection
				for _, dep := range result.Detections.Dependencies {
					deps = append(deps, expectedDetection(dep.BaseEntity))
				}
				for _, exp := range result.Detections.Exposures {
					exps = append(exps, expectedDetection(exp.BaseEntity))
				}
				if !sameDetections(deps, test.Dependencies) || !sameDetections(exps, test.Exposures) {
					result.Error = fmt.Sprintf("detection mismatch: dependencies want %+v got %+v; exposures want %+v got %+v", test.Dependencies, deps, test.Exposures, exps)
				} else {
					result.Passed = true
				}
			}
		}
		results = append(results, result)
	}
	return results
}

func expectedDetection(entity model.BaseEntity) ExpectedDetection {
	return ExpectedDetection{Type: entity.Type, Target: fmt.Sprint(entity.Details["target"]), File: entity.Locations[0].File, Line: entity.Locations[0].StartLine}
}

func sameDetections(actual, expected []ExpectedDetection) bool {
	if len(actual) != len(expected) {
		return false
	}
	counts := map[ExpectedDetection]int{}
	for _, item := range actual {
		counts[item]++
	}
	for _, item := range expected {
		counts[item]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func normalizeIdentity(identity *model.ServiceIdentity) {
	if len(identity.Aliases) == 0 {
		identity.Aliases = nil
	}
	if len(identity.Resources) == 0 {
		identity.Resources = nil
	}
	if len(identity.Metadata) == 0 {
		identity.Metadata = nil
	}
	sort.Slice(identity.Aliases, func(i, j int) bool {
		if identity.Aliases[i].Kind != identity.Aliases[j].Kind {
			return identity.Aliases[i].Kind < identity.Aliases[j].Kind
		}
		return identity.Aliases[i].Value < identity.Aliases[j].Value
	})
	sort.Slice(identity.Resources, func(i, j int) bool {
		if identity.Resources[i].Kind != identity.Resources[j].Kind {
			return identity.Resources[i].Kind < identity.Resources[j].Kind
		}
		if identity.Resources[i].Identifier != identity.Resources[j].Identifier {
			return identity.Resources[i].Identifier < identity.Resources[j].Identifier
		}
		return identity.Resources[i].Role < identity.Resources[j].Role
	})
}
