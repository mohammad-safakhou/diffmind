package golden

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
)

type options struct {
	ReportPath string
	GoldenPath string
	Update     bool
}

type corpusReport struct {
	Cases []corpusCase `json:"cases"`
}

type corpusCase struct {
	Name         string         `json:"name"`
	Status       string         `json:"status"`
	EntityCount  int            `json:"entity_count"`
	CountsByType map[string]int `json:"counts_by_type"`
}

type summary struct {
	Cases []summaryCase `json:"cases"`
}

type summaryCase struct {
	Name         string         `json:"name"`
	Status       string         `json:"status"`
	EntityCount  int            `json:"entity_count"`
	CountsByType map[string]int `json:"counts_by_type"`
}

func Run(_ context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}

	rep, err := readReport(opts.ReportPath)
	if err != nil {
		return err
	}
	current := buildSummary(rep)

	if opts.Update {
		if err := writeJSON(opts.GoldenPath, current); err != nil {
			return err
		}
		fmt.Println(opts.GoldenPath)
		return nil
	}

	expected, err := readSummary(opts.GoldenPath)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, current) {
		return fmt.Errorf("golden mismatch for %s; run with --update to accept changes", opts.GoldenPath)
	}
	fmt.Println("golden check passed")
	return nil
}

func parseOptions(args []string) (options, error) {
	fs := flag.NewFlagSet("golden", flag.ContinueOnError)
	reportPath := fs.String("report", filepath.Join(".diffmind", "corpus", "report.json"), "Path to corpus report JSON")
	goldenPath := fs.String("golden", filepath.Join("corpus", "golden", "summary.json"), "Path to golden summary JSON")
	update := fs.Bool("update", false, "Update golden summary from report")
	if err := fs.Parse(filterArgs(args)); err != nil {
		return options{}, fmt.Errorf("parse golden flags: %w", err)
	}
	return options{
		ReportPath: strings.TrimSpace(*reportPath),
		GoldenPath: strings.TrimSpace(*goldenPath),
		Update:     *update,
	}, nil
}

func filterArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--report" || arg == "--golden":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case arg == "--update":
			out = append(out, arg)
		case strings.HasPrefix(arg, "--report=") || strings.HasPrefix(arg, "--golden=") || strings.HasPrefix(arg, "--update="):
			out = append(out, arg)
		}
	}
	return out
}

func readReport(path string) (corpusReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return corpusReport{}, fmt.Errorf("read report %s: %w", path, err)
	}
	var rep corpusReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return corpusReport{}, fmt.Errorf("decode report %s: %w", path, err)
	}
	return rep, nil
}

func readSummary(path string) (summary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return summary{}, fmt.Errorf("read golden summary %s: %w", path, err)
	}
	var s summary
	if err := json.Unmarshal(data, &s); err != nil {
		return summary{}, fmt.Errorf("decode golden summary %s: %w", path, err)
	}
	return s, nil
}

func buildSummary(rep corpusReport) summary {
	out := summary{Cases: make([]summaryCase, 0, len(rep.Cases))}
	for _, c := range rep.Cases {
		out.Cases = append(out.Cases, summaryCase{
			Name:         c.Name,
			Status:       c.Status,
			EntityCount:  c.EntityCount,
			CountsByType: c.CountsByType,
		})
	}
	sort.Slice(out.Cases, func(i, j int) bool { return out.Cases[i].Name < out.Cases[j].Name })
	return out
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create golden output dir: %w", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal golden summary: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write golden summary %s: %w", path, err)
	}
	return nil
}
