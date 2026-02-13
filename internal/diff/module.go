package diff

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"diffmind/internal/bundleio"
)

type options struct {
	From   string
	To     string
	Format string
}

type Report struct {
	FromSnapshot string            `json:"from_snapshot"`
	ToSnapshot   string            `json:"to_snapshot"`
	Added        int               `json:"added"`
	Removed      int               `json:"removed"`
	Changed      int               `json:"changed"`
	Unchanged    int               `json:"unchanged"`
	ByType       map[string]Counts `json:"by_type"`
	AddedItems   []EntityDelta     `json:"added_items"`
	RemovedItems []EntityDelta     `json:"removed_items"`
	ChangedItems []EntityChange    `json:"changed_items"`
}

type Counts struct {
	Added     int `json:"added"`
	Removed   int `json:"removed"`
	Changed   int `json:"changed"`
	Unchanged int `json:"unchanged"`
}

type EntityDelta struct {
	Type       string `json:"type"`
	NaturalKey string `json:"natural_key"`
	ID         string `json:"id"`
}

type EntityChange struct {
	Type              string  `json:"type"`
	NaturalKey        string  `json:"natural_key"`
	FromID            string  `json:"from_id"`
	ToID              string  `json:"to_id"`
	FromEvidence      int     `json:"from_evidence"`
	ToEvidence        int     `json:"to_evidence"`
	FromConfidence    float64 `json:"from_confidence"`
	ToConfidence      float64 `json:"to_confidence"`
	AttributesChanged bool    `json:"attributes_changed"`
}

func Run(_ context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}

	from, err := bundleio.Load(opts.From)
	if err != nil {
		return err
	}
	to, err := bundleio.Load(opts.To)
	if err != nil {
		return err
	}

	r := BuildReport(from, to)
	switch opts.Format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	case "table":
		printTable(r)
		return nil
	default:
		return fmt.Errorf("unsupported format %q", opts.Format)
	}
}

func parseOptions(args []string) (options, error) {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	from := fs.String("from", filepath.Join(".diffmind", "bundle", "intelligence_bundle.json"), "From bundle path")
	to := fs.String("to", filepath.Join(".diffmind", "bundle", "intelligence_bundle.json"), "To bundle path")
	format := fs.String("format", "table", "Output format: table|json")

	if err := fs.Parse(filterArgs(args)); err != nil {
		return options{}, fmt.Errorf("parse diff flags: %w", err)
	}
	f := strings.ToLower(strings.TrimSpace(*format))
	if f != "table" && f != "json" {
		return options{}, fmt.Errorf("unsupported format %q", f)
	}
	return options{From: *from, To: *to, Format: f}, nil
}

func filterArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--from" || arg == "--to" || arg == "--format":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case strings.HasPrefix(arg, "--from=") || strings.HasPrefix(arg, "--to=") || strings.HasPrefix(arg, "--format="):
			out = append(out, arg)
		}
	}
	return out
}

func BuildReport(from bundleio.Bundle, to bundleio.Bundle) Report {
	r := Report{
		FromSnapshot: from.SnapshotID,
		ToSnapshot:   to.SnapshotID,
		ByType:       map[string]Counts{},
		AddedItems:   []EntityDelta{},
		RemovedItems: []EntityDelta{},
		ChangedItems: []EntityChange{},
	}

	fromMap := indexByKey(from.Entities)
	toMap := indexByKey(to.Entities)
	keys := unionKeys(fromMap, toMap)

	for _, key := range keys {
		f, okF := fromMap[key]
		t, okT := toMap[key]
		split := strings.SplitN(key, "|", 2)
		typeName := split[0]
		c := r.ByType[typeName]

		switch {
		case !okF && okT:
			r.Added++
			c.Added++
			r.AddedItems = append(r.AddedItems, EntityDelta{Type: t.Type, NaturalKey: t.NaturalKey, ID: t.ID})
		case okF && !okT:
			r.Removed++
			c.Removed++
			r.RemovedItems = append(r.RemovedItems, EntityDelta{Type: f.Type, NaturalKey: f.NaturalKey, ID: f.ID})
		default:
			changed := entityChanged(f, t)
			if changed {
				r.Changed++
				c.Changed++
				r.ChangedItems = append(r.ChangedItems, EntityChange{
					Type:              f.Type,
					NaturalKey:        f.NaturalKey,
					FromID:            f.ID,
					ToID:              t.ID,
					FromEvidence:      len(f.EvidenceIDs),
					ToEvidence:        len(t.EvidenceIDs),
					FromConfidence:    f.Confidence,
					ToConfidence:      t.Confidence,
					AttributesChanged: !reflect.DeepEqual(normalizeMap(f.Attributes), normalizeMap(t.Attributes)),
				})
			} else {
				r.Unchanged++
				c.Unchanged++
			}
		}
		r.ByType[typeName] = c
	}

	sort.Slice(r.AddedItems, func(i, j int) bool { return itemKey(r.AddedItems[i]) < itemKey(r.AddedItems[j]) })
	sort.Slice(r.RemovedItems, func(i, j int) bool { return itemKey(r.RemovedItems[i]) < itemKey(r.RemovedItems[j]) })
	sort.Slice(r.ChangedItems, func(i, j int) bool { return changeKey(r.ChangedItems[i]) < changeKey(r.ChangedItems[j]) })
	return r
}

func indexByKey(items []bundleio.Entity) map[string]bundleio.Entity {
	out := make(map[string]bundleio.Entity, len(items))
	for _, e := range items {
		out[e.Type+"|"+e.NaturalKey] = e
	}
	return out
}

func unionKeys(a map[string]bundleio.Entity, b map[string]bundleio.Entity) []string {
	set := map[string]struct{}{}
	for k := range a {
		set[k] = struct{}{}
	}
	for k := range b {
		set[k] = struct{}{}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func entityChanged(a bundleio.Entity, b bundleio.Entity) bool {
	if a.Confidence != b.Confidence {
		return true
	}
	if !reflect.DeepEqual(normalizeStrings(a.EvidenceIDs), normalizeStrings(b.EvidenceIDs)) {
		return true
	}
	if !reflect.DeepEqual(normalizeMap(a.Attributes), normalizeMap(b.Attributes)) {
		return true
	}
	return false
}

func normalizeStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func normalizeMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func printTable(r Report) {
	fmt.Printf("from: %s\n", r.FromSnapshot)
	fmt.Printf("to: %s\n", r.ToSnapshot)
	fmt.Printf("added: %d  removed: %d  changed: %d  unchanged: %d\n\n", r.Added, r.Removed, r.Changed, r.Unchanged)

	types := make([]string, 0, len(r.ByType))
	for t := range r.ByType {
		types = append(types, t)
	}
	sort.Strings(types)
	fmt.Println("by type:")
	fmt.Printf("%-14s  %-6s  %-7s  %-7s  %-9s\n", "TYPE", "ADDED", "REMOVED", "CHANGED", "UNCHANGED")
	for _, t := range types {
		c := r.ByType[t]
		fmt.Printf("%-14s  %-6d  %-7d  %-7d  %-9d\n", t, c.Added, c.Removed, c.Changed, c.Unchanged)
	}
}

func itemKey(i EntityDelta) string {
	return i.Type + "|" + i.NaturalKey + "|" + i.ID
}

func changeKey(c EntityChange) string {
	return c.Type + "|" + c.NaturalKey + "|" + c.FromID + "|" + c.ToID
}
