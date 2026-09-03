package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/backup"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/config"
)

const backupUsage = `Usage:
  diffmind backup create --output FILE --offline [--max-bytes N] [--json]
  diffmind backup verify --archive FILE [--sha256 DIGEST] [--max-bytes N] [--json]
  diffmind backup restore --archive FILE --destination NEW_DIRECTORY --offline [--sha256 DIGEST] [--allow-path-mismatch] [--max-bytes N] [--json]
  diffmind backup rotate --directory PRIVATE_DIRECTORY --keep-last N --offline [--max-bytes N] [--json]
  diffmind backup list --directory PRIVATE_DIRECTORY [--max-bytes N] [--json]

Stop all Diffmind processes and other writers before create/restore. --offline
acknowledges this requirement (old binaries do not honor maintenance locks).
Archives include all data under DIFFMIND_HOME, potentially source and secrets.
Restore never overwrites an existing destination or rewrites stored paths.
The default expanded-byte limit is 100 GiB. Format version and file checksums
are checked before restoration is published. See docs/backup-recovery.md.
Rotate verifies a new snapshot before permanently removing older catalog-owned
backups. It never prunes workspace history. Directory must exist with mode 0700
and be empty on first rotation. --keep-last is required (1..1000).
`

func runBackup(args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		_, err := fmt.Fprint(stdout, backupUsage)
		return err
	}
	if args[0] == "rotate" || args[0] == "list" {
		return runManagedBackup(args, stdout)
	}
	fs := flag.NewFlagSet("backup "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "print JSON report")
	maxBytes := fs.Int64("max-bytes", backup.DefaultMaxBytes, "maximum expanded bytes")
	var output, archive, destination, expected string
	var offline, allow bool
	switch args[0] {
	case "create":
		fs.StringVar(&output, "output", "", "new archive path")
		fs.BoolVar(&offline, "offline", false, "confirm all writers stopped")
	case "verify", "restore":
		fs.StringVar(&archive, "archive", "", "archive path")
		fs.StringVar(&expected, "sha256", "", "trusted archive checksum")
		if args[0] == "restore" {
			fs.StringVar(&destination, "destination", "", "new directory")
			fs.BoolVar(&offline, "offline", false, "confirm all writers stopped")
			fs.BoolVar(&allow, "allow-path-mismatch", false, "allow recovery drill at another path without rewriting stored paths")
		}
	default:
		return errors.New(backupUsage)
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err = fmt.Fprint(stdout, backupUsage)
		}
		return err
	}
	if fs.NArg() != 0 || *maxBytes <= 0 {
		return errors.New(backupUsage)
	}
	if args[0] != "verify" && !offline {
		return errors.New("--offline is required: stop all Diffmind processes, older binaries, and other writers first")
	}
	var r backup.Report
	var err error
	switch args[0] {
	case "create":
		if output == "" {
			return errors.New("--output is required")
		}
		r, err = backup.Create(config.Home(), output, version, *maxBytes)
	case "verify":
		if archive == "" {
			return errors.New("--archive is required")
		}
		r, err = backup.Verify(archive, expected, *maxBytes)
	case "restore":
		if archive == "" || destination == "" {
			return errors.New("--archive and --destination are required")
		}
		r, err = backup.Restore(archive, destination, expected, *maxBytes, allow)
	}
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(stdout).Encode(r)
	}
	_, err = fmt.Fprintf(stdout, "Backup %s complete: format=%d entries=%d bytes=%d\nSource home: %s\nSHA-256: %s\n", args[0], r.Format, r.Entries, r.Bytes, r.Home, r.SHA256)
	if err == nil && args[0] == "restore" && allow {
		_, err = fmt.Fprintln(stdout, "Stored absolute paths are unchanged. Do not start workers at a different home until repository/pack paths are reviewed.")
	}
	return err
}

func runManagedBackup(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("backup "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	directory := fs.String("directory", "", "private managed backup directory")
	maxBytes := fs.Int64("max-bytes", backup.DefaultMaxBytes, "maximum expanded bytes per snapshot")
	jsonOutput := fs.Bool("json", false, "print JSON report")
	var keep int
	var offline bool
	if args[0] == "rotate" {
		fs.IntVar(&keep, "keep-last", 0, "number of verified snapshots to retain")
		fs.BoolVar(&offline, "offline", false, "confirm all writers stopped")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err = fmt.Fprint(stdout, backupUsage)
		}
		return err
	}
	if fs.NArg() != 0 || *directory == "" || *maxBytes <= 0 {
		return errors.New(backupUsage)
	}
	if args[0] == "list" {
		snapshots, err := backup.ManagedList(config.Home(), *directory, *maxBytes)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(stdout).Encode(snapshots)
		}
		for _, snapshot := range snapshots {
			if _, err := fmt.Fprintf(stdout, "%s %s %s\n", snapshot.Report.Created.Format("2006-01-02T15:04:05Z"), snapshot.Report.SHA256, snapshot.Archive); err != nil {
				return err
			}
		}
		return nil
	}
	if !offline {
		return errors.New("--offline is required: stop all writers before backup rotation")
	}
	result, err := backup.Rotate(config.Home(), *directory, version, keep, *maxBytes)
	if err != nil {
		if result.Created.ID != "" {
			return fmt.Errorf("snapshot %s saved; rotation incomplete (%d old snapshots removed): %w", result.Created.Archive, len(result.Removed), err)
		}
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(stdout).Encode(result)
	}
	_, err = fmt.Fprintf(stdout, "Verified snapshot: %s\nSHA-256: %s\nRetained: %d; permanently removed old snapshots: %d\n", result.Created.Archive, result.Created.Report.SHA256, len(result.Retained), len(result.Removed))
	return err
}
