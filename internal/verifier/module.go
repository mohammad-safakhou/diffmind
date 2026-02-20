package verifier

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"diffmind/internal/consolidation"
)

func Run(_ context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}
	if opts.OutBundle == "" {
		opts.OutBundle = filepath.Join(opts.OutDir, "bundle", "intelligence_bundle.json")
	}
	if opts.ReviewQueuePath == "" {
		opts.ReviewQueuePath = filepath.Join(opts.OutDir, "verify", "review_queue.json")
	}

	bundle, err := readIntelligenceBundle(opts.InBundle)
	if err != nil {
		return err
	}
	slog.Info("verification input loaded", "in_bundle", opts.InBundle, "entities", len(bundle.Entities))
	verified, report, reviewQueue, err := verify(bundle, opts)
	if err != nil {
		return err
	}
	slog.Info("verification decisions computed", "snapshot_id", report.SnapshotID, "decision_entities_added", report.DecisionEntitiesAdded)

	if err := writeJSON(opts.OutBundle, verified); err != nil {
		return err
	}
	reportPath := filepath.Join(opts.OutDir, "verify", "report.json")
	if err := writeJSON(reportPath, report); err != nil {
		return err
	}
	if err := writeJSON(opts.ReviewQueuePath, map[string]any{
		"generated_at":     report.GeneratedAt,
		"snapshot_id":      report.SnapshotID,
		"verifier_id":      report.VerifierID,
		"verifier_version": report.VerifierVersion,
		"items":            reviewQueue,
	}); err != nil {
		return err
	}

	slog.Info("verification completed",
		"snapshot_id", report.SnapshotID,
		"input_entities", report.InputEntities,
		"output_entities", report.OutputEntities,
		"verified", report.VerifiedCount,
		"needs_review", report.NeedsReviewCount,
		"disputed", report.DisputedCount,
		"review_queue_items", report.ReviewQueueItems,
		"missing_evidence_critical", report.MissingEvidenceCritical,
		"hypothesis_candidates", report.HypothesisCandidates,
		"contradiction_disputes", report.ContradictionDisputes,
		"bundle_path", opts.OutBundle,
		"report_path", reportPath,
		"review_queue_path", opts.ReviewQueuePath,
	)
	fmt.Println(opts.OutBundle)
	return nil
}

func parseOptions(args []string) (Options, error) {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	in := fs.String("in", filepath.Join(".diffmind", "bundle", "intelligence_bundle.json"), "Input intelligence bundle path")
	outDir := fs.String("out", ".diffmind", "Output root")
	outBundle := fs.String("out-bundle", "", "Verified intelligence bundle output path (default: overwrite bundle/intelligence_bundle.json)")
	reviewQueuePath := fs.String("review-queue", "", "Review queue output path (default: <out>/verify/review_queue.json)")
	promote := fs.Float64("promote-threshold", 0.9, "Confidence threshold to mark entity as verified")
	dispute := fs.Float64("dispute-threshold", 0.7, "Confidence threshold below which entity is disputed")
	strictEvidence := fs.Bool("strict-evidence", true, "Require evidence IDs for critical claims (endpoint/external/dependency surfaces)")
	twoPass := fs.Bool("two-pass", true, "Enable hypothesis + contradiction verification passes")
	if err := fs.Parse(filterVerifyArgs(args)); err != nil {
		return Options{}, fmt.Errorf("parse verify flags: %w", err)
	}
	return Options{
		InBundle:         strings.TrimSpace(*in),
		OutDir:           strings.TrimSpace(*outDir),
		OutBundle:        strings.TrimSpace(*outBundle),
		ReviewQueuePath:  strings.TrimSpace(*reviewQueuePath),
		PromoteThreshold: *promote,
		DisputeThreshold: *dispute,
		StrictEvidence:   *strictEvidence,
		TwoPass:          *twoPass,
	}, nil
}

func filterVerifyArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--strict-evidence":
			out = append(out, arg)
		case arg == "--two-pass":
			out = append(out, arg)
		case arg == "--in" || arg == "--out" || arg == "--out-bundle" || arg == "--review-queue" || arg == "--promote-threshold" || arg == "--dispute-threshold":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case strings.HasPrefix(arg, "--in=") ||
			strings.HasPrefix(arg, "--out=") ||
			strings.HasPrefix(arg, "--out-bundle=") ||
			strings.HasPrefix(arg, "--review-queue=") ||
			strings.HasPrefix(arg, "--promote-threshold=") ||
			strings.HasPrefix(arg, "--dispute-threshold=") ||
			strings.HasPrefix(arg, "--strict-evidence=") ||
			strings.HasPrefix(arg, "--two-pass="):
			out = append(out, arg)
		}
	}
	return out
}

func readIntelligenceBundle(path string) (consolidation.IntelligenceBundle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return consolidation.IntelligenceBundle{}, fmt.Errorf("read intelligence bundle %s: %w", path, err)
	}
	var b consolidation.IntelligenceBundle
	if err := json.Unmarshal(data, &b); err != nil {
		return consolidation.IntelligenceBundle{}, fmt.Errorf("decode intelligence bundle: %w", err)
	}
	return b, nil
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
