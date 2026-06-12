package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
)

// RenderTable writes a human-readable per-objective + overall table, followed
// by the offending false positives / false negatives so a miss is one glance
// from its cause.
func RenderTable(w io.Writer, rep Report) {
	fmt.Fprintf(w, "FIXTURE %s  MODE %s\n", rep.Fixture, rep.Mode)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "objective\tTP\tFP\tFN\tP\tR\tF1")
	for _, o := range rep.Objectives {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%.2f\t%.2f\t%.2f\n", o.Objective, o.TP, o.FP, o.FN, o.Precision, o.Recall, o.F1)
	}
	fmt.Fprintf(tw, "OVERALL (micro)\t%d\t%d\t%d\t%.2f\t%.2f\t%.2f\n",
		rep.Overall.TP, rep.Overall.FP, rep.Overall.FN, rep.Overall.Precision, rep.Overall.Recall, rep.Overall.F1)
	tw.Flush()

	for _, o := range rep.Objectives {
		for _, fp := range o.FalsePositives {
			fmt.Fprintf(w, "  FP %-14s %s\n", o.Objective, fpName(fp))
		}
		for _, fn := range o.FalseNegatives {
			fmt.Fprintf(w, "  FN %-14s %s\n", o.Objective, fpName(fn))
		}
		for _, im := range o.InstanceMismatches {
			fmt.Fprintf(w, "  INSTANCE %-8s %s  want %q got %q\n", o.Objective, im.Key, im.Want, im.Got)
		}
	}
}

func fpName(r ItemRef) string {
	if r.Name != "" {
		return r.Name + "  [" + r.Key + "]"
	}
	return r.Key
}

// WriteJSON emits the machine-readable report.
func WriteJSON(w io.Writer, rep Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// RenderVariance writes a human-readable per-objective stability table for a
// K-run variance report. core/union is the headline column (1.0 = perfectly
// reproducible); counts exposes per-run count volatility.
func RenderVariance(w io.Writer, rep VarianceReport) {
	fmt.Fprintf(w, "VARIANCE over %d runs", rep.Runs)
	if len(rep.RunIDs) > 0 {
		fmt.Fprintf(w, " [%s]", strings.Join(rep.RunIDs, ", "))
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "objective\tcounts\tmean\tstdev\tcore/union\tjaccard")
	for _, o := range rep.Objectives {
		fmt.Fprintf(tw, "%s\t%s\t%.1f\t%.2f\t%d/%d=%.2f\t%.2f\n",
			o.Objective, formatCounts(o.Counts), o.Mean, o.Stdev,
			o.CoreKeys, o.UnionKeys, o.CoreUnion, o.JaccardMean)
	}
	tw.Flush()
}

func formatCounts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// WriteVarianceJSON emits the machine-readable variance report.
func WriteVarianceJSON(w io.Writer, rep VarianceReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// RenderFloorCoverage writes the per-objective floor↔LLM overlap table. The
// coverage column shows "n/a" when the LLM run found nothing of that type (so
// an empty bucket never reads as 1.0).
func RenderFloorCoverage(w io.Writer, rep FloorCoverageReport) {
	fmt.Fprintf(w, "FLOOR COVERAGE  repo=%s", rep.Repo)
	if rep.RunID != "" {
		fmt.Fprintf(w, "  run=%s", rep.RunID)
	}
	fmt.Fprintln(w, "  (overlap with the LLM run, NOT recall vs ground truth)")
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "objective\tfloor\tllm\tcovered\tfloor_only\tcoverage")
	for _, o := range rep.Objectives {
		cov := "n/a"
		if o.Applicable {
			cov = fmt.Sprintf("%.2f", o.Coverage)
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%s\n",
			o.Objective, o.FloorKeys, o.LLMKeys, o.Covered, o.FloorOnly, cov)
	}
	tw.Flush()
}

// WriteFloorCoverageJSON emits the machine-readable floor-coverage report.
func WriteFloorCoverageJSON(w io.Writer, rep FloorCoverageReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}
