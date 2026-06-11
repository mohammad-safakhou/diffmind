// Package reconcile implements the final deterministic pipeline stage.
package reconcile

import (
	"sort"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	reconcilepolicy "github.com/mohammad-safakhou/diffmind/internal/reconcile"
)

type Runner struct{}

type Input struct {
	Exposures    []model.Exposure
	Dependencies []model.Dependency
	Connections  []model.Connection
	Unresolved   []model.UnresolvedItem
}

type Output struct {
	Exposures    []model.Exposure
	Dependencies []model.Dependency
	Connections  []model.Connection
	Unresolved   []model.UnresolvedItem
}

func (Runner) Run(input Input) Output {
	exposures := reconcilepolicy.DedupeExposures(input.Exposures)
	dependencies := reconcilepolicy.DedupeDependencies(input.Dependencies)
	connections, orphaned := reconcilepolicy.FilterConnections(input.Connections, exposures, dependencies)
	unresolved := append(append([]model.UnresolvedItem(nil), input.Unresolved...), orphaned...)

	sort.Slice(exposures, func(i, j int) bool { return exposures[i].ID < exposures[j].ID })
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].ID < dependencies[j].ID })
	sort.Slice(connections, func(i, j int) bool { return connections[i].ID < connections[j].ID })

	return Output{
		Exposures:    exposures,
		Dependencies: dependencies,
		Connections:  connections,
		Unresolved:   unresolved,
	}
}
