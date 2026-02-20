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
	BundlePath         string
	View               string
	Format             string
	TypeFilter         string
	VerificationFilter string
	QueryText          string
	ConfidenceMin      float64
	Limit              int
	Offset             int
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

	rows := filterEntitiesWithOptions(b.Entities, opts)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Type == rows[j].Type {
			return rows[i].NaturalKey < rows[j].NaturalKey
		}
		return rows[i].Type < rows[j].Type
	})
	total := len(rows)
	rows = paginateEntities(rows, opts.Offset, opts.Limit)

	switch opts.Format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"snapshot_id": b.SnapshotID,
			"view":        opts.View,
			"total":       total,
			"offset":      opts.Offset,
			"limit":       opts.Limit,
			"count":       len(rows),
			"entities":    rows,
		})
	case "table":
		printTable(b.SnapshotID, opts, len(rows), total, rows)
		return nil
	default:
		return fmt.Errorf("unsupported format %q", opts.Format)
	}
}

func parseOptions(args []string) (options, error) {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	bundlePath := fs.String("bundle", filepath.Join(".diffmind", "bundle", "intelligence_bundle.json"), "Canonical intelligence bundle path")
	view := fs.String("view", "all", "View: all|runtime|endpoints|config|external|pipeline|infra|dependency|ownership|risk|conflict|verify")
	format := fs.String("format", "table", "Output format: table|json")
	typeFilter := fs.String("type", "", "Optional exact entity type filter")
	verification := fs.String("verification", "", "Optional verification status filter (verified|needs_review|disputed|inferred)")
	queryText := fs.String("q", "", "Optional free-text filter over natural key and attributes")
	confidenceMin := fs.Float64("confidence-min", 0, "Optional minimum confidence threshold")
	limit := fs.Int("limit", 0, "Optional max results to return")
	offset := fs.Int("offset", 0, "Optional result offset before limit")

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
	if *confidenceMin < 0 || *confidenceMin > 1 {
		return options{}, fmt.Errorf("confidence-min must be between 0 and 1")
	}
	if *limit < 0 {
		return options{}, fmt.Errorf("limit must be >= 0")
	}
	if *offset < 0 {
		return options{}, fmt.Errorf("offset must be >= 0")
	}
	return options{
		BundlePath:         *bundlePath,
		View:               v,
		Format:             f,
		TypeFilter:         strings.TrimSpace(*typeFilter),
		VerificationFilter: strings.ToLower(strings.TrimSpace(*verification)),
		QueryText:          strings.ToLower(strings.TrimSpace(*queryText)),
		ConfidenceMin:      *confidenceMin,
		Limit:              *limit,
		Offset:             *offset,
	}, nil
}

func filterQueryArgs(args []string) []string {
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--bundle" || arg == "--view" || arg == "--format" || arg == "--type" || arg == "--verification" || arg == "--q" || arg == "--confidence-min" || arg == "--limit" || arg == "--offset":
			filtered = append(filtered, arg)
			if i+1 < len(args) {
				i++
				filtered = append(filtered, args[i])
			}
		case strings.HasPrefix(arg, "--bundle=") || strings.HasPrefix(arg, "--view=") || strings.HasPrefix(arg, "--format=") || strings.HasPrefix(arg, "--type=") || strings.HasPrefix(arg, "--verification=") || strings.HasPrefix(arg, "--q=") || strings.HasPrefix(arg, "--confidence-min=") || strings.HasPrefix(arg, "--limit=") || strings.HasPrefix(arg, "--offset="):
			filtered = append(filtered, arg)
		}
	}
	return filtered
}

func FilterEntities(in []bundleio.Entity, view string) []bundleio.Entity {
	return filterEntitiesWithOptions(in, options{View: view})
}

func filterEntitiesWithOptions(in []bundleio.Entity, opts options) []bundleio.Entity {
	targetType, _ := viewType(opts.View)
	out := make([]bundleio.Entity, 0, len(in))
	for _, e := range in {
		if targetType != "" && e.Type != targetType {
			continue
		}
		if opts.TypeFilter != "" && !strings.EqualFold(strings.TrimSpace(opts.TypeFilter), strings.TrimSpace(e.Type)) {
			continue
		}
		if opts.ConfidenceMin > 0 && e.Confidence < opts.ConfidenceMin {
			continue
		}
		if opts.VerificationFilter != "" {
			status := strings.ToLower(strings.TrimSpace(attrString(e.Attributes, "verification_status")))
			if status != opts.VerificationFilter {
				continue
			}
		}
		if opts.QueryText != "" {
			hay := strings.ToLower(e.Type + " " + e.NaturalKey + " " + flattenedAttrs(e.Attributes))
			if !strings.Contains(hay, opts.QueryText) {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

func paginateEntities(in []bundleio.Entity, offset, limit int) []bundleio.Entity {
	if offset >= len(in) {
		return []bundleio.Entity{}
	}
	if offset > 0 {
		in = in[offset:]
	}
	if limit > 0 && len(in) > limit {
		return append([]bundleio.Entity(nil), in[:limit]...)
	}
	return append([]bundleio.Entity(nil), in...)
}

func ValidateView(view string) bool {
	_, ok := viewType(view)
	return ok
}

func viewType(view string) (string, bool) {
	mapping := map[string]string{
		"all":        "",
		"runtime":    "RuntimeUnit",
		"endpoints":  "Endpoint",
		"config":     "ConfigKey",
		"external":   "ExternalCall",
		"pipeline":   "PipelineStep",
		"infra":      "InfraResource",
		"dependency": "Dependency",
		"ownership":  "OwnershipRule",
		"risk":       "DependencyRisk",
		"conflict":   "Conflict",
		"verify":     "VerificationDecision",
	}
	t, ok := mapping[view]
	return t, ok
}

func printTable(snapshotID string, opts options, count int, total int, rows []bundleio.Entity) {
	fmt.Printf("snapshot: %s\n", snapshotID)
	fmt.Printf("view: %s\n", opts.View)
	if opts.TypeFilter != "" {
		fmt.Printf("type: %s\n", opts.TypeFilter)
	}
	if opts.VerificationFilter != "" {
		fmt.Printf("verification: %s\n", opts.VerificationFilter)
	}
	if opts.QueryText != "" {
		fmt.Printf("q: %s\n", opts.QueryText)
	}
	if opts.ConfidenceMin > 0 {
		fmt.Printf("confidence_min: %.2f\n", opts.ConfidenceMin)
	}
	if opts.Offset > 0 || opts.Limit > 0 {
		fmt.Printf("offset: %d\n", opts.Offset)
		fmt.Printf("limit: %d\n", opts.Limit)
		fmt.Printf("total: %d\n", total)
	}
	fmt.Printf("count: %d\n\n", count)
	fmt.Printf("%-64s  %-14s  %-8s  %-8s  %s\n", "ID", "TYPE", "EVIDENCE", "CONF", "SUMMARY")
	for _, e := range rows {
		summary := summarize(e)
		fmt.Printf("%-64s  %-14s  %-8d  %-8.2f  %s\n", e.ID, e.Type, len(e.EvidenceIDs), e.Confidence, summary)
	}
}

func flattenedAttrs(attrs map[string]any) string {
	if len(attrs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+fmt.Sprint(attrs[k]))
	}
	return strings.Join(parts, " ")
}

func attrString(attrs map[string]any, key string) string {
	if attrs == nil {
		return ""
	}
	v, ok := attrs[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
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
	case "Dependency":
		return fmt.Sprintf("%v@%v [%v]", a["name"], a["version"], a["ecosystem"])
	case "OwnershipRule":
		return fmt.Sprintf("%v -> %v", a["pattern"], a["owner"])
	case "DependencyRisk":
		return fmt.Sprintf("%v %v (%v)", a["name"], a["risk_type"], a["severity"])
	case "Conflict":
		return fmt.Sprintf("%v %v", a["entity_type"], a["status"])
	case "VerificationDecision":
		return fmt.Sprintf("%v -> %v", a["subject_entity_id"], a["status"])
	default:
		return e.NaturalKey
	}
}
