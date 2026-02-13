package httpapi

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"diffmind/internal/bundleio"
	"diffmind/internal/diff"
	"diffmind/internal/query"
)

type options struct {
	Addr       string
	BundlePath string
}

func Run(ctx context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              opts.Addr,
		Handler:           newMux(opts.BundlePath),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("serve http api: %w", err)
	}
	return nil
}

func parseOptions(args []string) (options, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "HTTP listen address")
	bundle := fs.String("bundle", filepath.Join(".diffmind", "bundle", "intelligence_bundle.json"), "Default canonical intelligence bundle path")

	if err := fs.Parse(filterArgs(args)); err != nil {
		return options{}, fmt.Errorf("parse serve flags: %w", err)
	}
	return options{
		Addr:       strings.TrimSpace(*addr),
		BundlePath: strings.TrimSpace(*bundle),
	}, nil
}

func filterArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--addr" || arg == "--bundle":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case strings.HasPrefix(arg, "--addr=") || strings.HasPrefix(arg, "--bundle="):
			out = append(out, arg)
		}
	}
	return out
}

func newMux(defaultBundlePath string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/entities", handleEntities(defaultBundlePath))
	mux.HandleFunc("/diff", handleDiff)
	return mux
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
	})
}

func handleEntities(defaultBundlePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		view := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view")))
		if view == "" {
			view = "all"
		}
		if !query.ValidateView(view) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported view %q", view))
			return
		}

		bundlePath := strings.TrimSpace(r.URL.Query().Get("bundle"))
		if bundlePath == "" {
			bundlePath = defaultBundlePath
		}

		b, err := bundleio.Load(bundlePath)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		rows := query.FilterEntities(b.Entities, view)
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Type == rows[j].Type {
				return rows[i].NaturalKey < rows[j].NaturalKey
			}
			return rows[i].Type < rows[j].Type
		})

		writeJSON(w, http.StatusOK, map[string]any{
			"snapshot_id": b.SnapshotID,
			"view":        view,
			"count":       len(rows),
			"entities":    rows,
		})
	}
}

func handleDiff(w http.ResponseWriter, r *http.Request) {
	fromPath := strings.TrimSpace(r.URL.Query().Get("from"))
	toPath := strings.TrimSpace(r.URL.Query().Get("to"))
	if fromPath == "" || toPath == "" {
		writeError(w, http.StatusBadRequest, "missing required query params: from and to")
		return
	}

	fromBundle, err := bundleio.Load(fromPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	toBundle, err := bundleio.Load(toPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, diff.BuildReport(fromBundle, toBundle))
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"error": message,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
