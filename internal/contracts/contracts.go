package contracts

import (
	"context"
)

// Module is the common executable contract for independent pipeline stages.
type Module interface {
	Run(context.Context, []string) error
}

type SnapshotModule interface{ Module }
type ClassifierModule interface{ Module }
type ParserModule interface{ Module }
type AnalyzerModule interface{ Module }
type ConsolidationModule interface{ Module }
