package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"diffmind/internal/contracts"
)

type options struct {
	GraphID          string
	ClaimsPath       string
	ObservationsPath string
	OutPath          string
}

type Module struct{}

func (Module) Run(ctx context.Context, args []string) error { return Run(ctx, args) }

var _ contracts.Module = Module{}

func Run(_ context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("runtime subcommand is required: plan|reconcile")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "plan":
		return runPlan(args[1:])
	case "reconcile":
		return runReconcile(args[1:])
	default:
		return fmt.Errorf("unsupported runtime subcommand %q", args[0])
	}
}

func runPlan(args []string) error {
	fs := flag.NewFlagSet("runtime plan", flag.ContinueOnError)
	outPath := fs.String("out", filepath.Join(".diffmind", "runtime", "plan.json"), "Runtime reconciliation plan output path")
	if err := fs.Parse(filterArgs(args)); err != nil {
		return fmt.Errorf("parse runtime plan flags: %w", err)
	}
	plan := DefaultPlan()
	if err := writeJSON(strings.TrimSpace(*outPath), plan); err != nil {
		return err
	}
	fmt.Println(strings.TrimSpace(*outPath))
	return nil
}

func runReconcile(args []string) error {
	opts, err := parseReconcileOptions(args)
	if err != nil {
		return err
	}

	claims, err := readClaims(opts.ClaimsPath)
	if err != nil {
		return err
	}
	observations, err := readObservations(opts.ObservationsPath)
	if err != nil {
		return err
	}
	result, err := Reconcile(context.Background(), contracts.RuntimeReconciliationRequest{
		GraphID:      opts.GraphID,
		Claims:       claims,
		Observations: observations,
	})
	if err != nil {
		return err
	}
	if err := writeJSON(opts.OutPath, result); err != nil {
		return err
	}
	fmt.Println(opts.OutPath)
	return nil
}

func parseReconcileOptions(args []string) (options, error) {
	fs := flag.NewFlagSet("runtime reconcile", flag.ContinueOnError)
	graphID := fs.String("graph-id", "", "Graph ID for reconciliation request")
	claims := fs.String("claims", filepath.Join(".diffmind", "runtime", "claims.json"), "Runtime claims input JSON")
	observations := fs.String("observations", filepath.Join(".diffmind", "runtime", "observations.json"), "Runtime observations input JSON")
	out := fs.String("out", filepath.Join(".diffmind", "runtime", "reconcile_result.json"), "Reconciliation result output JSON")
	if err := fs.Parse(filterArgs(args)); err != nil {
		return options{}, fmt.Errorf("parse runtime reconcile flags: %w", err)
	}
	if strings.TrimSpace(*graphID) == "" {
		return options{}, errors.New("--graph-id is required")
	}
	return options{
		GraphID:          strings.TrimSpace(*graphID),
		ClaimsPath:       strings.TrimSpace(*claims),
		ObservationsPath: strings.TrimSpace(*observations),
		OutPath:          strings.TrimSpace(*out),
	}, nil
}

type semanticReconciler struct{}

var _ contracts.RuntimeReconciliationModule = semanticReconciler{}

func (semanticReconciler) Reconcile(_ context.Context, req contracts.RuntimeReconciliationRequest) (contracts.RuntimeReconciliationResult, error) {
	confirmed := map[string]struct{}{}
	contradicted := map[string]struct{}{}
	unmapped := map[string]struct{}{}
	needsReview := map[string]struct{}{}

	claimKeys := map[string]contracts.RuntimeClaim{}
	for _, c := range req.Claims {
		key := claimKey(c)
		if key == "" {
			continue
		}
		claimKeys[key] = c
		needsReview[key] = struct{}{}
	}

	for i, obs := range req.Observations {
		keys := observationKeys(obs)
		matched := false
		contradiction := isContradiction(obs)
		for _, k := range keys {
			if _, ok := claimKeys[k]; !ok {
				continue
			}
			matched = true
			if contradiction {
				contradicted[k] = struct{}{}
				delete(confirmed, k)
			} else if _, blocked := contradicted[k]; !blocked {
				confirmed[k] = struct{}{}
			}
			delete(needsReview, k)
		}
		if !matched {
			unmapped[fmt.Sprintf("observation:%d", i)] = struct{}{}
		}
	}

	return contracts.RuntimeReconciliationResult{
		GraphID:      req.GraphID,
		Confirmed:    setToSortedSlice(confirmed),
		Contradicted: setToSortedSlice(contradicted),
		Unmapped:     setToSortedSlice(unmapped),
		NeedsReview:  setToSortedSlice(needsReview),
	}, nil
}

// Reconcile executes phase-2 runtime reconciliation logic.
func Reconcile(ctx context.Context, req contracts.RuntimeReconciliationRequest) (contracts.RuntimeReconciliationResult, error) {
	engine := semanticReconciler{}
	return engine.Reconcile(ctx, req)
}

func claimKey(c contracts.RuntimeClaim) string {
	if v := strings.TrimSpace(c.EdgeID); v != "" {
		return "edge:" + v
	}
	if v := strings.TrimSpace(c.NodeID); v != "" {
		return "node:" + v
	}
	return ""
}

func observationKeys(o contracts.RuntimeObservation) []string {
	if o.Attributes == nil {
		return nil
	}
	out := []string{}
	if v := strings.TrimSpace(o.Attributes["edge_id"]); v != "" {
		out = append(out, "edge:"+v)
	}
	if v := strings.TrimSpace(o.Attributes["node_id"]); v != "" {
		out = append(out, "node:"+v)
	}
	if v := strings.TrimSpace(o.Attributes["claim_id"]); v != "" {
		if strings.HasPrefix(v, "edge:") || strings.HasPrefix(v, "node:") {
			out = append(out, v)
		}
	}
	return out
}

func isContradiction(o contracts.RuntimeObservation) bool {
	if o.Attributes == nil {
		return false
	}
	v := strings.ToLower(strings.TrimSpace(o.Attributes["contradicts"]))
	return v == "1" || v == "true" || v == "yes"
}

func setToSortedSlice(in map[string]struct{}) []string {
	out := make([]string, 0, len(in))
	for k := range in {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func readClaims(path string) ([]contracts.RuntimeClaim, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read runtime claims %s: %w", path, err)
	}
	var claims []contracts.RuntimeClaim
	if err := json.Unmarshal(data, &claims); err != nil {
		return nil, fmt.Errorf("decode runtime claims %s: %w", path, err)
	}
	return claims, nil
}

func readObservations(path string) ([]contracts.RuntimeObservation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read runtime observations %s: %w", path, err)
	}
	var observations []contracts.RuntimeObservation
	if err := json.Unmarshal(data, &observations); err != nil {
		return nil, fmt.Errorf("decode runtime observations %s: %w", path, err)
	}
	return observations, nil
}

// DefaultPlan returns the runtime reconciliation readiness contract.
func DefaultPlan() contracts.RuntimeReconciliationPlan {
	return contracts.RuntimeReconciliationPlan{
		ContractVersion: "v0",
		Phase:           "phase2-preparation",
		Enabled:         false,
		PublishBlocking: false,
		InputSignals: []string{
			"api_gateway_access_logs",
			"queue_broker_delivery_events",
			"runtime_db_query_observations",
			"service_dependency_telemetry",
		},
		MatchStrategy: "canonical_id_and_evidence_overlap",
		OutputStates: []string{
			"confirmed",
			"contradicted",
			"missing_runtime_signal",
			"runtime_only_unmapped",
		},
	}
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime output: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write runtime output %s: %w", path, err)
	}
	return nil
}

func filterArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--out" || arg == "--graph-id" || arg == "--claims" || arg == "--observations":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case strings.HasPrefix(arg, "--out=") || strings.HasPrefix(arg, "--graph-id=") || strings.HasPrefix(arg, "--claims=") || strings.HasPrefix(arg, "--observations="):
			out = append(out, arg)
		}
	}
	return out
}
