package ops

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"diffmind/internal/audit"
)

type sloReport struct {
	GeneratedAtUTC      string             `json:"generated_at_utc"`
	AvailabilityPercent float64            `json:"availability_percent"`
	LatencyP95MS        float64            `json:"latency_p95_ms"`
	IntegrityIncidents  int                `json:"integrity_incidents"`
	ByAction            map[string]float64 `json:"by_action"`
	Passed              bool               `json:"passed"`
}

type qualityReport struct {
	Metrics struct {
		PassRate float64 `json:"pass_rate"`
	} `json:"metrics"`
}

type rolloutPlan struct {
	GeneratedAtUTC  string   `json:"generated_at_utc"`
	Component       string   `json:"component"`
	Candidate       string   `json:"candidate_version"`
	Current         string   `json:"current_version"`
	Steps           []string `json:"steps"`
	RollbackVersion string   `json:"rollback_version"`
}

func Run(_ context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("ops subcommand is required: slo|backup|restore|rollout")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "slo":
		return runSLO(args[1:])
	case "backup":
		return runBackup(args[1:])
	case "restore":
		return runRestore(args[1:])
	case "rollout":
		return runRollout(args[1:])
	default:
		return fmt.Errorf("unsupported ops subcommand %q", args[0])
	}
}

func runSLO(args []string) error {
	fs := flag.NewFlagSet("ops slo", flag.ContinueOnError)
	auditRoot := fs.String("audit-root", ".diffmind", "Root containing audit/events")
	qualityPath := fs.String("quality", filepath.Join(".diffmind", "quality", "report.json"), "Quality report path")
	outPath := fs.String("out", filepath.Join(".diffmind", "ops", "slo_report.json"), "SLO report output")
	if err := fs.Parse(filterArgs(args)); err != nil {
		return fmt.Errorf("parse ops slo flags: %w", err)
	}
	rep, err := evaluateSLO(strings.TrimSpace(*auditRoot), strings.TrimSpace(*qualityPath))
	if err != nil {
		return err
	}
	if err := writeJSON(strings.TrimSpace(*outPath), rep); err != nil {
		return err
	}
	fmt.Println(strings.TrimSpace(*outPath))
	if !rep.Passed {
		return errors.New("ops slo failed")
	}
	return nil
}

func evaluateSLO(root string, qualityPath string) (sloReport, error) {
	events, err := audit.ListEvents(root, "", 100000)
	if err != nil {
		return sloReport{}, err
	}
	total := 0
	success := 0
	integrityIncidents := 0
	actionTotal := map[string]int{}
	actionSuccess := map[string]int{}
	latencies := make([]float64, 0, len(events))
	for _, e := range events {
		if strings.TrimSpace(e.Action) == "" {
			continue
		}
		total++
		actionTotal[e.Action]++
		if strings.EqualFold(strings.TrimSpace(e.Decision), "allow") {
			success++
			actionSuccess[e.Action]++
		}
		if v, ok := e.Metadata["duration_ms"]; ok {
			if f, ok := toFloat(v); ok {
				latencies = append(latencies, f)
			}
		}
		if strings.Contains(strings.ToLower(strings.TrimSpace(e.Reason)), "integrity") {
			integrityIncidents++
		}
	}
	availability := 100.0
	if total > 0 {
		availability = (float64(success) / float64(total)) * 100
	}
	byAction := map[string]float64{}
	for action, n := range actionTotal {
		if n == 0 {
			continue
		}
		byAction[action] = (float64(actionSuccess[action]) / float64(n)) * 100
	}
	p95 := percentile(latencies, 95)
	qr := qualityReport{}
	if data, err := os.ReadFile(qualityPath); err == nil {
		_ = json.Unmarshal(data, &qr)
	}
	passed := availability >= 99.9 && integrityIncidents == 0 && qr.Metrics.PassRate >= 0.95
	return sloReport{
		GeneratedAtUTC:      time.Now().UTC().Format(time.RFC3339),
		AvailabilityPercent: availability,
		LatencyP95MS:        p95,
		IntegrityIncidents:  integrityIncidents,
		ByAction:            byAction,
		Passed:              passed,
	}, nil
}

func runBackup(args []string) error {
	fs := flag.NewFlagSet("ops backup", flag.ContinueOnError)
	sourceRoot := fs.String("source", ".diffmind", "Source root to backup")
	outPath := fs.String("out", filepath.Join(".diffmind", "ops", fmt.Sprintf("backup-%d.tar.gz", time.Now().UTC().Unix())), "Backup archive path")
	if err := fs.Parse(filterArgs(args)); err != nil {
		return fmt.Errorf("parse ops backup flags: %w", err)
	}
	if err := createBackup(strings.TrimSpace(*sourceRoot), strings.TrimSpace(*outPath)); err != nil {
		return err
	}
	fmt.Println(strings.TrimSpace(*outPath))
	return nil
}

func createBackup(sourceRoot string, outPath string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gw := gzip.NewWriter(f)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	return filepath.Walk(sourceRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		rf, err := os.Open(path)
		if err != nil {
			return err
		}
		defer rf.Close()
		if _, err := io.Copy(tw, rf); err != nil {
			return err
		}
		return nil
	})
}

func runRestore(args []string) error {
	fs := flag.NewFlagSet("ops restore", flag.ContinueOnError)
	archivePath := fs.String("archive", "", "Backup archive path")
	targetRoot := fs.String("target", ".diffmind", "Restore target root")
	if err := fs.Parse(filterArgs(args)); err != nil {
		return fmt.Errorf("parse ops restore flags: %w", err)
	}
	if strings.TrimSpace(*archivePath) == "" {
		return errors.New("--archive is required")
	}
	if err := restoreBackup(strings.TrimSpace(*archivePath), strings.TrimSpace(*targetRoot)); err != nil {
		return err
	}
	fmt.Println(strings.TrimSpace(*targetRoot))
	return nil
}

func restoreBackup(archivePath string, targetRoot string) error {
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return err
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "..") {
			return fmt.Errorf("invalid archive entry %q", hdr.Name)
		}
		outPath := filepath.Join(targetRoot, clean)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
		wf, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
		if err != nil {
			return err
		}
		if _, err := io.Copy(wf, tr); err != nil {
			wf.Close()
			return err
		}
		if err := wf.Close(); err != nil {
			return err
		}
	}
	return nil
}

func runRollout(args []string) error {
	fs := flag.NewFlagSet("ops rollout", flag.ContinueOnError)
	component := fs.String("component", "extractor", "Component name")
	candidate := fs.String("candidate", "", "Candidate version")
	current := fs.String("current", "unknown", "Current stable version")
	outPath := fs.String("out", filepath.Join(".diffmind", "ops", "rollout_plan.json"), "Rollout plan output")
	if err := fs.Parse(filterArgs(args)); err != nil {
		return fmt.Errorf("parse ops rollout flags: %w", err)
	}
	if strings.TrimSpace(*candidate) == "" {
		return errors.New("--candidate is required")
	}
	plan := rolloutPlan{
		GeneratedAtUTC:  time.Now().UTC().Format(time.RFC3339),
		Component:       strings.TrimSpace(*component),
		Candidate:       strings.TrimSpace(*candidate),
		Current:         strings.TrimSpace(*current),
		RollbackVersion: strings.TrimSpace(*current),
		Steps: []string{
			"validate quality gate and SLO gate in staging",
			"deploy canary to 5% traffic",
			"observe p95 latency, error rate, integrity incidents for 30m",
			"increase to 25%, then 100% if stable",
			"on regression, rollback immediately to previous stable version",
		},
	}
	if err := writeJSON(strings.TrimSpace(*outPath), plan); err != nil {
		return err
	}
	fmt.Println(strings.TrimSpace(*outPath))
	return nil
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func percentile(values []float64, p int) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	idx := int((float64(p) / 100.0) * float64(len(sorted)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func filterArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--audit-root" || arg == "--quality" || arg == "--out" || arg == "--source" || arg == "--archive" || arg == "--target" || arg == "--component" || arg == "--candidate" || arg == "--current":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case strings.HasPrefix(arg, "--audit-root=") || strings.HasPrefix(arg, "--quality=") || strings.HasPrefix(arg, "--out=") || strings.HasPrefix(arg, "--source=") || strings.HasPrefix(arg, "--archive=") || strings.HasPrefix(arg, "--target=") || strings.HasPrefix(arg, "--component=") || strings.HasPrefix(arg, "--candidate=") || strings.HasPrefix(arg, "--current="):
			out = append(out, arg)
		}
	}
	return out
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
