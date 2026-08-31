package indexer

import (
	"fmt"
	"strings"
)

// buildRunArgs assembles the `docker run` command line.
//
// PERMISSIONS
//   - --user 0:0    we run as root inside the container so indexers
//     that create files in /root/.m2 (Maven cache),
//     /root/.gradle, etc. don't run into chown errors.
//     The container is ephemeral; host files are only
//     touched via the explicit volume mounts.
//   - The source mount is :ro to enforce read-only access from the
//     container side.
//
// MOUNTS
//   - SourcePath →   /sources:ro
//   - OutputPath →   /output (rw)
//   - ExtraMounts →  user-defined
func (d *DockerIndexer) buildRunArgs(req RunRequest, image string) []string {
	args := []string{
		"run",
		"--rm",
		"--user", "0:0",
		"--init", // PID 1 reaper so timed-out subprocesses don't zombie
	}

	if req.NetworkMode != "" {
		args = append(args, "--network", req.NetworkMode)
	}

	args = append(args,
		"-v", fmt.Sprintf("%s:/sources:ro", req.SourcePath),
		"-v", fmt.Sprintf("%s:/output", req.OutputPath),
	)

	for host, container := range req.ExtraMounts {
		args = append(args, "-v", fmt.Sprintf("%s:%s", host, container))
	}

	args = append(args, "-e", "DIFFMIND_INDEXER_OUTPUT=/output")
	for k, v := range req.ExtraEnv {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	args = append(args, image)

	args = append(args,
		"--source", "/sources",
		"--output", "/output/index.scip",
	)
	if len(req.Languages) > 0 {
		args = append(args, "--languages", strings.Join(req.Languages, ","))
	}
	if req.PerIndexerTimeout > 0 {
		args = append(args, "--timeout", req.PerIndexerTimeout.String())
	}
	if req.Parallel > 0 {
		args = append(args, "--parallel", fmt.Sprintf("%d", req.Parallel))
	}
	if req.KeepWork {
		args = append(args, "--keep-work")
	}
	return args
}
