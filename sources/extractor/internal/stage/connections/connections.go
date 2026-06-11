// Package connections owns Stage 4 selection between the AST connection
// engine and the deterministic shallow fallback.
package connections

import (
	"context"
	"sort"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

type AggregateReporter func(totalExposures, connections, withoutPaths int, mode string)

type Runner struct {
	Report AggregateReporter
}

type Input struct {
	Index         *ast.ProjectIndex
	Exposures     []model.Exposure
	Dependencies  []model.Dependency
	MinConfidence float64
	Workers       int
	Progress      func()
}

type Output struct {
	Connections []model.Connection
	Unresolved  []model.UnresolvedItem
}

func (r Runner) Run(ctx context.Context, input Input) Output {
	if len(input.Exposures) == 0 || len(input.Dependencies) == 0 {
		return Output{}
	}
	if input.Index != nil && (len(input.Index.Symbols) > 0 || len(input.Index.Frameworks) > 0) {
		connections, unresolved := runASTConnections(
			ctx, input.Index, input.Exposures, input.Dependencies,
			input.MinConfidence, input.Workers, input.Progress,
		)
		withoutPaths := exposuresWithoutConnections(input.Exposures, connections)
		if len(connections) > 0 || len(unresolved) > 0 {
			r.report(len(input.Exposures), len(connections), withoutPaths, "ast")
			sort.Slice(connections, func(i, j int) bool { return connections[i].ID < connections[j].ID })
			return Output{Connections: connections, Unresolved: unresolved}
		}
		util.Warn("agents.connections", "ast walk produced no connections; falling back to shallow matcher", nil)
	}

	util.Warn("agents.connections", "no ast index available; using shallow name matcher", nil)
	connections, unresolved := buildShallowConnections(input.Exposures, input.Dependencies, input.MinConfidence)
	r.report(len(input.Exposures), len(connections), 0, "no_index")
	if input.Progress != nil {
		for range input.Exposures {
			input.Progress()
		}
	}
	return Output{Connections: connections, Unresolved: unresolved}
}

func (r Runner) report(totalExposures, connections, withoutPaths int, mode string) {
	if r.Report != nil {
		r.Report(totalExposures, connections, withoutPaths, mode)
	}
}

func exposuresWithoutConnections(exposures []model.Exposure, connections []model.Connection) int {
	connected := make(map[string]struct{}, len(connections))
	for _, connection := range connections {
		connected[connection.FromExposureID] = struct{}{}
	}
	total := 0
	for _, exposure := range exposures {
		if _, ok := connected[exposure.ID]; !ok {
			total++
		}
	}
	return total
}
