package consolidation

import (
	"context"

	"diffmind/internal/contracts"
)

type Module struct{}

func (Module) Run(ctx context.Context, args []string) error {
	return Run(ctx, args)
}

var _ contracts.ConsolidationModule = Module{}
