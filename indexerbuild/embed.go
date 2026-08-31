// Package indexerbuild ships the build context for the
// diffmind-indexer Docker image as an embedded filesystem.
//
// # WHY THIS PACKAGE EXISTS AT MODULE ROOT
//
// go:embed directives cannot reference parent directories. The
// canonical sources we embed live at the module root
// (Dockerfile.indexer, go.mod, go.sum) and in cmd/diffmind-index/
// (a sibling of this package). We therefore put the embed file
// at module root level — under a dedicated package whose only job
// is to expose a single embed.FS.
//
// DiffMind's indexer driver imports this package and extracts the FS
// to a temp build-context directory on first run, then shells out to
// `docker build`. The whole flow is transparent to the user: the CLI
// detects that the image is missing, builds it inline, and proceeds
// with the run. No "step 1: build image" gymnastics required.
//
// CONTENTS
//
//   - Dockerfile.indexer  the multi-stage Dockerfile (root of the tree)
//   - wrapper/**          the Go sources of the in-image entrypoint
//     binary. Has its own go.mod (stdlib-only) so
//     it builds standalone inside the wrapper-builder
//     container stage without depending on diffmind's
//     own module graph.
//
// # SIZE
//
// The whole embed is well under 200 KB. It does NOT include Go module
// caches, node_modules, indexer binaries — those are downloaded by the
// Dockerfile's builder stages over network. We do NOT vendor third-party
// indexers into the binary.
package indexerbuild

import "embed"

// Context is the embedded build context for the diffmind-indexer
// image. Consumers obtain the filesystem and copy its contents to a
// real directory on disk before invoking `docker build`.
//
//go:embed Dockerfile.indexer wrapper
var Context embed.FS

// DockerfileName is the file name inside Context to pass to
// `docker build -f`. Centralised so the indexer driver and any
// developer-facing diagnostics agree on the spelling.
const DockerfileName = "Dockerfile.indexer"
