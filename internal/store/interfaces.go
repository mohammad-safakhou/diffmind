package store

import (
	"context"

	"diffmind/internal/facts"
	"diffmind/internal/graphschema"
)

type BlobStore interface {
	PutFile(ctx context.Context, objectKey string, sourcePath string) error
	EnsureBucket(ctx context.Context) error
}

type SnapshotMetadataStore interface {
	PersistSnapshot(ctx context.Context, snap SnapshotMetadataRecord, inventory []FileInventoryRow) error
	Close() error
}

type FactBundleStore interface {
	PersistBundle(ctx context.Context, bundle facts.Bundle) error
}

type GraphBundleStore interface {
	PersistGraph(ctx context.Context, graph graphschema.Graph, artifactPath string) error
}
