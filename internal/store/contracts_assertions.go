package store

var _ SnapshotMetadataStore = (*PostgresSnapshotStore)(nil)
var _ FactBundleStore = (*FactStore)(nil)
var _ GraphBundleStore = (*GraphStore)(nil)
var _ BlobStore = (*MinIOBlobStore)(nil)
