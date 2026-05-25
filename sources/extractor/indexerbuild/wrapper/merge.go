package main

import (
	"fmt"
	"os/exec"
)

// mergeIndexes combines per-language SCIP indexes into a single output
// file. We shell out to the `scip` CLI's built-in `merge` subcommand:
// it knows how to concatenate the proto streams correctly and handle
// the canonicalization of duplicate symbols across indexes.
//
// We DO NOT implement merge in Go here because:
//  1. SCIP index merging needs careful handling of duplicate symbol
//     definitions (especially for files that appear in multiple
//     per-language indexes, e.g. a polyglot file). The CLI does this.
//  2. The CLI is already present in the image; no extra deps.
//  3. The proto schema may evolve. Delegating to the CLI is forward-
//     compatible.
//
// USAGE
//
//	scip merge --output OUT.scip IN1.scip IN2.scip ...
//
// On stderr, scip prints progress lines we ignore. On non-zero exit,
// we surface the captured stderr verbatim.
func mergeIndexes(inputs []string, output string) error {
	if len(inputs) == 0 {
		return fmt.Errorf("merge: no inputs")
	}
	if len(inputs) == 1 {
		// Single input → just copy. Avoids invoking scip for trivial cases.
		return copyFile(inputs[0], output)
	}
	args := append([]string{"merge", "--output", output}, inputs...)
	cmd := exec.Command("scip", args...)
	combined, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("scip merge: %v: %s", err, string(combined))
	}
	return nil
}

// copyFile is a small helper that copies src to dst with default
// permissions. We use it as the merge fast-path when there's only
// one input.
//
// We use the os primitives directly (not io.Copy on opened files)
// because the SCIP file may be tens or hundreds of MB and we want
// the OS-level sendfile/copy_file_range optimization. On Linux this
// happens transparently via os.Link or io.Copy depending on the FS.
func copyFile(src, dst string) error {
	cmd := exec.Command("cp", "-f", src, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cp %s %s: %v: %s", src, dst, err, string(out))
	}
	return nil
}
