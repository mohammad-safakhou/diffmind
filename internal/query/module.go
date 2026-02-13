package query

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"diffmind/internal/bundleio"
)

type options struct {
	BundlePath string
	View       string
	Format     string
}

func Run(_ context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}

	b, err := bundleio.Load(opts.BundlePath)
	if err != nil {
		return err
	}

	rows := FilterEntities(b.Entities, opts.View)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Type == rows[j].Type {
			return rows[i].NaturalKey < rows[j].NaturalKey
		}
		return rows[i].Type < rows[j].Type
	})

	switch opts.Format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"snapshot_id": b.SnapshotID,
			"view":        opts.View,
			"count":       len(rows),
			"entities":    rows,
		})
	case "table":
		printTable(b.SnapshotID, opts.View, rows)
		return nil
	default:
		return fmt.Errorf("unsupported format %q", opts.Format)
	}
}

func parseOptions(args []string) (options, error) {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	bundlePath := fs.String("bundle", filepath.Join(".diffmind", "bundle", "intelligence_bundle.json"), "Canonical intelligence bundle path")
	view := fs.String("view", "all", "View: all|runtime|endpoints|config|external|pipeline|infra")
	format := fs.String("format", "table", "Output format: table|json")

	if err := fs.Parse(filterQueryArgs(args)); err != nil {
		return options{}, fmt.Errorf("parse query flags: %w", err)
	}
	v := strings.ToLower(strings.TrimSpace(*view))
	if !ValidateView(v) {
		return options{}, fmt.Errorf("unsupported view %q", v)
	}
	f := strings.ToLower(strings.TrimSpace(*format))
	if f != "table" && f != "json" {
		return options{}, fmt.Errorf("unsupported format %q", f)
	}
	return options{BundlePath: *bundlePath, View: v, Format: f}, nil
}

func filterQueryArgs(args []string) []string {
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--bundle" || arg == "--view" || arg == "--format":
			filtered = append(filtered, arg)
			if i+1 < len(args) {
				i++
				filtered = append(filtered, args[i])
			}
		case strings.HasPrefix(arg, "--bundle=") || strings.HasPrefix(arg, "--view=") || strings.HasPrefix(arg, "--format="):
			filtered = append(filtered, arg)
		}
	}
	return filtered
}

func FilterEntities(in []bundleio.Entity, view string) []bundleio.Entity {
	if view == "all" {
		return append([]bundleio.Entity(nil), in...)
	}
	targetType, _ := viewType(view)
	out := make([]bundleio.Entity, 0, len(in))
	for _, e := range in {
		if e.Type == targetType {
			out = append(out, e)
		}
	}
	return out
}

func ValidateView(view string) bool {
	_, ok := viewType(view)
	return ok
}

func viewType(view string) (string, bool) {
	mapping := map[string]string{
		"all":       "",
		"runtime":   "RuntimeUnit",
		"endpoints": "Endpoint",
		"config":    "ConfigKey",
		"external":  "ExternalCall",
		"pipeline":  "PipelineStep",
		"infra":     "InfraResource",
	}
	t, ok := mapping[view]
	return t, ok
}

func printTable(snapshotID string, view string, rows []bundleio.Entity) {
	fmt.Printf("snapshot: %s\n", snapshotID)
	fmt.Printf("view: %s\n", view)
	fmt.Printf("count: %d\n\n", len(rows))
	fmt.Printf("%-64s  %-14s  %-8s  %-8s  %s\n", "ID", "TYPE", "EVIDENCE", "CONF", "SUMMARY")
	for _, e := range rows {
		summary := summarize(e)
		fmt.Printf("%-64s  %-14s  %-8d  %-8.2f  %s\n", e.ID, e.Type, len(e.EvidenceIDs), e.Confidence, summary)
	}
}

func summarize(e bundleio.Entity) string {
	a := e.Attributes
	switch e.Type {
	case "RuntimeUnit":
		return fmt.Sprintf("%v %v (%v)", a["language"], a["kind"], a["file"])
	case "Endpoint":
		return fmt.Sprintf("%v %v [%v]", a["method"], a["path"], a["framework"])
	case "ConfigKey":
		return fmt.Sprintf("%v via %v", a["key"], a["pattern"])
	case "ExternalCall":
		return fmt.Sprintf("%v %v via %v", a["method"], a["target"], a["library"])
	case "PipelineStep":
		return fmt.Sprintf("%v %v", a["provider"], a["value"])
	case "InfraResource":
		if a["resource_type"] != nil {
			return fmt.Sprintf("%v %v", a["provider"], a["resource_type"])
		}
		if a["kind"] != nil {
			return fmt.Sprintf("%v %v", a["provider"], a["kind"])
		}
		return fmt.Sprintf("%v", a["provider"])
	default:
		return e.NaturalKey
	}
}
