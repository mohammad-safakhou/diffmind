package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/config"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/homelock"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

const storageUsage = `Usage:
  diffmind storage migrate --offline [--json]
  diffmind storage verify --offline [--json]

Stop every Diffmind process first. Migrate imports the JSON refresh queue into
indexed SQLite, preserving job IDs, attempts, timestamps and original JSON files.
Migration is opt-in. After migration, queue/queue.sqlite is authoritative: never
run an older binary or remove queue/ to downgrade. Back up before migrating.
Project metadata, graph artifacts and ingestion history remain on the filesystem.
This does not enable distributed workers or multiple server writers.
`

func runStorage(args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		_, err := fmt.Fprint(stdout, storageUsage)
		return err
	}
	if args[0] != "migrate" && args[0] != "verify" {
		return errors.New(storageUsage)
	}
	fs := flag.NewFlagSet("storage "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	offline := fs.Bool("offline", false, "confirm all writers stopped")
	asJSON := fs.Bool("json", false, "print JSON report")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err = fmt.Fprint(stdout, storageUsage)
		}
		return err
	}
	if !*offline || fs.NArg() != 0 {
		return errors.New("--offline is required; stop all Diffmind processes before storage maintenance")
	}
	var report store.QueueReport
	var err error
	if args[0] == "migrate" {
		report, err = store.MigrateQueue(config.Home())
	} else {
		release, e := homelock.Acquire(config.Home(), true)
		if e != nil {
			return e
		}
		defer release()
		st, e := store.New(config.Home())
		if e != nil {
			return e
		}
		report, err = st.VerifyQueue()
	}
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(stdout).Encode(report)
	}
	_, err = fmt.Fprintf(stdout, "Queue %s complete: backend=%s schema=%d jobs=%d attempts=%d\n", args[0], report.Backend, report.Schema, report.Jobs, report.Attempts)
	return err
}
