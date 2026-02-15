package graph

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"diffmind/internal/bundleio"
	"go.yaml.in/yaml/v3"
)

type discoverOptions struct {
	SourcesCSV  string
	ManifestOut string
}

type discoveredCall struct {
	sourceServiceID string
	scheme          string
	host            string
}

func runDiscover(_ context.Context, args []string) error {
	opts, err := parseDiscoverOptions(args)
	if err != nil {
		return err
	}
	sources := splitCSV(opts.SourcesCSV)
	if len(sources) == 0 {
		return errors.New("discover requires --sources")
	}
	services, err := discoverServices(sources)
	if err != nil {
		return err
	}
	if len(services) == 0 {
		return fmt.Errorf("discover found no extraction outputs under --sources=%q", opts.SourcesCSV)
	}
	if err := writeDiscoveredManifest(opts.ManifestOut, manifest{Services: services}); err != nil {
		return err
	}
	fmt.Println(opts.ManifestOut)
	return nil
}

func parseDiscoverOptions(args []string) (discoverOptions, error) {
	fs := flag.NewFlagSet("graph discover", flag.ContinueOnError)
	sources := fs.String("sources", "", "Comma-separated extraction output paths (or parent dirs to scan)")
	manifestOut := fs.String("manifest-out", filepath.Join("graph", "services.yaml"), "Output manifest path")
	if err := fs.Parse(filterDiscoverArgs(args)); err != nil {
		return discoverOptions{}, fmt.Errorf("parse graph discover flags: %w", err)
	}
	return discoverOptions{
		SourcesCSV:  strings.TrimSpace(*sources),
		ManifestOut: strings.TrimSpace(*manifestOut),
	}, nil
}

func filterDiscoverArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--sources" || arg == "--manifest-out":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case strings.HasPrefix(arg, "--sources=") || strings.HasPrefix(arg, "--manifest-out="):
			out = append(out, arg)
		}
	}
	return out
}

func discoverServices(sources []string) ([]serviceSpec, error) {
	outputRoots, err := discoverOutputRoots(sources)
	if err != nil {
		return nil, err
	}
	specs := make([]serviceSpec, 0, len(outputRoots))
	httpCalls := make([]discoveredCall, 0, 16)
	seenIDs := map[string]int{}

	for _, root := range outputRoots {
		bundlePath := filepath.Join(root, "bundle", "intelligence_bundle.json")
		bundle, err := bundleio.Load(bundlePath)
		if err != nil {
			return nil, err
		}

		repoPath := discoverRepoPathFromRunReport(root)
		baseID := filepathBase(repoPath)
		if strings.TrimSpace(baseID) == "" {
			baseID = filepathBase(root)
		}
		serviceID := ensureUniqueServiceID(sanitizeServiceID(baseID), seenIDs)

		spec := serviceSpec{
			ID:             serviceID,
			Name:           humanizeServiceName(serviceID),
			RepoPath:       repoPath,
			BundlePath:     bundlePath,
			AnalyzerBundle: discoverAnalyzerBundlePath(root),
		}

		for _, e := range bundle.Entities {
			if e.Type != "ExternalCall" {
				continue
			}
			protocol := strings.ToLower(strings.TrimSpace(fmt.Sprint(e.Attributes["protocol"])))
			target := strings.TrimSpace(fmt.Sprint(e.Attributes["target"]))
			method := strings.ToUpper(strings.TrimSpace(fmt.Sprint(e.Attributes["method"])))
			if target == "" {
				continue
			}
			switch protocol {
			case "queue":
				if method == "CONSUME" {
					spec.QueueConsumes = append(spec.QueueConsumes, target)
				} else {
					spec.QueuePublishes = append(spec.QueuePublishes, target)
				}
			case "db":
				if method == "READ" {
					spec.DBReads = append(spec.DBReads, target)
				} else {
					spec.DBWrites = append(spec.DBWrites, target)
				}
			case "http", "":
				scheme, host := parseTargetSchemeHost(target)
				if host != "" {
					httpCalls = append(httpCalls, discoveredCall{
						sourceServiceID: serviceID,
						scheme:          scheme,
						host:            host,
					})
				}
			}
		}

		spec.QueuePublishes = uniqueStrings(spec.QueuePublishes)
		spec.QueueConsumes = uniqueStrings(spec.QueueConsumes)
		spec.DBReads = uniqueStrings(spec.DBReads)
		spec.DBWrites = uniqueStrings(spec.DBWrites)
		specs = append(specs, spec)
	}

	assignBaseURLsFromHTTPCalls(specs, httpCalls)
	sort.Slice(specs, func(i, j int) bool { return specs[i].ID < specs[j].ID })
	return specs, nil
}

func discoverOutputRoots(sources []string) ([]string, error) {
	rootSet := map[string]struct{}{}
	roots := make([]string, 0, len(sources))
	for _, source := range sources {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		abs, err := filepath.Abs(source)
		if err != nil {
			return nil, fmt.Errorf("resolve source path %s: %w", source, err)
		}
		st, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("stat source path %s: %w", abs, err)
		}

		if !st.IsDir() {
			if filepath.Base(abs) == "intelligence_bundle.json" && filepath.Base(filepath.Dir(abs)) == "bundle" {
				root := filepath.Dir(filepath.Dir(abs))
				if _, exists := rootSet[root]; !exists {
					rootSet[root] = struct{}{}
					roots = append(roots, root)
				}
			}
			continue
		}

		knownBundle := filepath.Join(abs, "bundle", "intelligence_bundle.json")
		if fileExists(knownBundle) {
			if _, exists := rootSet[abs]; !exists {
				rootSet[abs] = struct{}{}
				roots = append(roots, abs)
			}
			continue
		}

		walkErr := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				switch name {
				case ".git", ".idea", "node_modules", "vendor":
					return filepath.SkipDir
				}
				return nil
			}
			if d.Name() != "intelligence_bundle.json" || filepath.Base(filepath.Dir(path)) != "bundle" {
				return nil
			}
			root := filepath.Dir(filepath.Dir(path))
			if _, exists := rootSet[root]; !exists {
				rootSet[root] = struct{}{}
				roots = append(roots, root)
			}
			return nil
		})
		if walkErr != nil {
			return nil, fmt.Errorf("scan source path %s: %w", abs, walkErr)
		}
	}
	sort.Strings(roots)
	return roots, nil
}

func discoverAnalyzerBundlePath(outputRoot string) string {
	path := filepath.Join(outputRoot, "analyzers", "bundle.json")
	if fileExists(path) {
		return path
	}
	return ""
}

func discoverRepoPathFromRunReport(outputRoot string) string {
	path := filepath.Join(outputRoot, "run", "report.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var report struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return ""
	}
	return strings.TrimSpace(report.Source)
}

func assignBaseURLsFromHTTPCalls(specs []serviceSpec, calls []discoveredCall) {
	for _, call := range calls {
		targetIdx := -1
		matchCount := 0
		normalizedHost := normalizeTarget(call.host)
		for i := range specs {
			if specs[i].ID == call.sourceServiceID {
				continue
			}
			for _, alias := range serviceAliases(specs[i]) {
				if alias != "" && strings.Contains(normalizedHost, alias) {
					targetIdx = i
					matchCount++
					break
				}
			}
		}
		// Add only when a single service clearly matches this host.
		if matchCount == 1 && targetIdx >= 0 {
			specs[targetIdx].BaseURLs = append(specs[targetIdx].BaseURLs, call.scheme+"://"+call.host)
		}
	}
	for i := range specs {
		specs[i].BaseURLs = uniqueStrings(specs[i].BaseURLs)
	}
}

func parseTargetSchemeHost(target string) (string, string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", ""
	}
	target = strings.Trim(target, "\"'")
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		u, err := url.Parse(target)
		if err != nil {
			return "", ""
		}
		return strings.ToLower(u.Scheme), strings.ToLower(u.Hostname())
	}
	// Best-effort parse for host/path targets without explicit scheme.
	if strings.Contains(target, "/") {
		parts := strings.Split(target, "/")
		if len(parts) > 0 {
			host := strings.ToLower(strings.TrimSpace(parts[0]))
			if strings.Contains(host, ".") {
				return "http", host
			}
		}
	}
	return "", ""
}

var serviceIDSanitizer = regexp.MustCompile(`[^a-z0-9_-]+`)
var multiDash = regexp.MustCompile(`-+`)

func sanitizeServiceID(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.ReplaceAll(v, " ", "-")
	v = serviceIDSanitizer.ReplaceAllString(v, "-")
	v = multiDash.ReplaceAllString(v, "-")
	v = strings.Trim(v, "-_")
	if v == "" {
		return "service"
	}
	return v
}

func ensureUniqueServiceID(id string, seen map[string]int) string {
	if _, exists := seen[id]; !exists {
		seen[id] = 1
		return id
	}
	seen[id]++
	return fmt.Sprintf("%s-%d", id, seen[id])
}

func humanizeServiceName(id string) string {
	parts := strings.FieldsFunc(id, func(r rune) bool {
		return r == '-' || r == '_'
	})
	if len(parts) == 0 {
		return id
	}
	for i := range parts {
		if parts[i] == "" {
			continue
		}
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, " ")
}

func writeDiscoveredManifest(path string, m manifest) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal discovered manifest: %w", err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create manifest dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write manifest %s: %w", path, err)
	}
	return nil
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
