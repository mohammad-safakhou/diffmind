package eval

import (
	"encoding/json"
	"fmt"
	"io"
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
