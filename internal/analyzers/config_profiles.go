package analyzers

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

var (
	reSpringProfileFile = regexp.MustCompile(`^application-([a-z0-9_\-]+)\.(yaml|yml|properties)$`)
	reSpringPlaceholder = regexp.MustCompile(`\$\{([^}:]+)(?::([^}]*))?\}`)
)

type springConfigEntry struct {
	Key             string
	RawValue        string
	SourceFile      string
	Line            int
	ProfileOverride string
}

type springValueRef struct {
	Key  string
	File sourceFile
	Line int
	Col  int
	Text string
}

func detectSpringProfileResolvedConfig(c *collector, files []sourceFile) {
	if c == nil || len(files) == 0 {
		return
	}
	byPath := make(map[string]sourceFile, len(files))
	byProfile := map[string][]springConfigEntry{}
	for _, f := range files {
		byPath[f.Path] = f
		profile, ok := springConfigManifestProfile(f.Path)
		if !ok {
			continue
		}
		for _, e := range parseSpringConfigEntries(f) {
			e.SourceFile = f.Path
			targetProfile := profile
			if strings.TrimSpace(e.ProfileOverride) != "" {
				targetProfile = strings.TrimSpace(e.ProfileOverride)
			}
			byProfile[targetProfile] = append(byProfile[targetProfile], e)
		}
	}
	if len(byProfile) == 0 {
		return
	}

	// Deterministic ordering of overlays by source path and key.
	for p := range byProfile {
		sort.Slice(byProfile[p], func(i, j int) bool {
			if byProfile[p][i].SourceFile == byProfile[p][j].SourceFile {
				return byProfile[p][i].Key < byProfile[p][j].Key
			}
			return byProfile[p][i].SourceFile < byProfile[p][j].SourceFile
		})
	}

	base := map[string]springConfigEntry{}
	for _, e := range byProfile["default"] {
		base[e.Key] = e
	}

	profiles := make([]string, 0, len(byProfile))
	for p := range byProfile {
		profiles = append(profiles, p)
	}
	sort.Strings(profiles)
	for _, profile := range profiles {
		merged := map[string]springConfigEntry{}
		for k, v := range base {
			merged[k] = v
		}
		if profile != "default" {
			for _, e := range byProfile[profile] {
				merged[e.Key] = e
			}
		}

		env := normalizeSpringProfile(profile)
		for _, key := range sortedKeys(merged) {
			entry := merged[key]
			f, ok := byPath[entry.SourceFile]
			if !ok {
				continue
			}
			line := entry.Line
			if line < 1 {
				line = 1
			}
			col := 1
			snippet := springLine(f, line)
			resolved, unresolved, placeholderVar, placeholderDefault, placeholderVars, placeholderDefaults, unresolvedVars := resolveSpringPlaceholderValue(entry.RawValue)
			sensitive := reSecretLike.MatchString(strings.ToLower(key))
			valueRef := entry.RawValue
			resolvedValue := resolved
			if sensitive {
				valueRef = "[REDACTED]"
				resolvedValue = "[REDACTED]"
			}
			c.addFactWithEvidence("ConfigKey", map[string]any{
				"key":                         key,
				"pattern":                     "spring_profile_resolved",
				"source_kind":                 "config_manifest",
				"environment":                 env,
				"profile":                     profile,
				"origin_file":                 entry.SourceFile,
				"value_ref":                   valueRef,
				"resolved_value":              resolvedValue,
				"resolved_value_hash":         hashString(resolved),
				"placeholder_var":             placeholderVar,
				"placeholder_default":         placeholderDefault,
				"placeholder_vars":            strings.Join(placeholderVars, ","),
				"placeholder_defaults":        strings.Join(placeholderDefaults, ","),
				"placeholder_unresolved_vars": strings.Join(unresolvedVars, ","),
				"placeholder_status":          springPlaceholderStatus(unresolved, len(unresolvedVars), len(placeholderVars)),
				"sensitive":                   sensitive,
				"file":                        entry.SourceFile,
			}, f, line, col, snippet, func() { c.report.ConfigKeys++ })
			if sensitive {
				c.addFactWithEvidence("SensitiveSurface", map[string]any{
					"kind":           "config_key",
					"key":            key,
					"classification": "secret-like",
					"source_kind":    "config_manifest",
					"environment":    env,
					"profile":        profile,
					"file":           entry.SourceFile,
				}, f, line, col, snippet, func() { c.report.SensitiveSurfaces++ })
			}
		}

		// Link code refs to resolved profile values.
		refs := collectSpringValueRefs(files)
		for _, ref := range refs {
			entry, ok := merged[ref.Key]
			if !ok {
				continue
			}
			resolved, unresolved, placeholderVar, placeholderDefault, placeholderVars, placeholderDefaults, unresolvedVars := resolveSpringPlaceholderValue(entry.RawValue)
			c.addFactWithEvidence("ConfigKey", map[string]any{
				"key":                         ref.Key,
				"pattern":                     "spring_code_ref_resolved",
				"source_kind":                 "resolved_link",
				"environment":                 env,
				"profile":                     profile,
				"origin_file":                 entry.SourceFile,
				"resolved_value_hash":         hashString(resolved),
				"placeholder_var":             placeholderVar,
				"placeholder_default":         placeholderDefault,
				"placeholder_vars":            strings.Join(placeholderVars, ","),
				"placeholder_defaults":        strings.Join(placeholderDefaults, ","),
				"placeholder_unresolved_vars": strings.Join(unresolvedVars, ","),
				"placeholder_status":          springPlaceholderStatus(unresolved, len(unresolvedVars), len(placeholderVars)),
				"ref_file":                    ref.File.Path,
			}, ref.File, ref.Line, ref.Col, ref.Text, func() { c.report.ConfigKeys++ })
		}
	}
}

func springConfigManifestProfile(path string) (string, bool) {
	base := strings.ToLower(strings.TrimSpace(filepath.Base(path)))
	switch base {
	case "application.yml", "application.yaml", "application.properties":
		return "default", true
	}
	if m := reSpringProfileFile.FindStringSubmatch(base); len(m) >= 2 {
		p := strings.TrimSpace(m[1])
		if p != "" {
			return p, true
		}
	}
	return "", false
}

func parseSpringConfigEntries(file sourceFile) []springConfigEntry {
	ext := strings.ToLower(strings.TrimSpace(file.Ext))
	switch ext {
	case ".properties":
		return parsePropertiesEntries(file)
	case ".yaml", ".yml":
		return parseYAMLEntries(file)
	default:
		return nil
	}
}

func parsePropertiesEntries(file sourceFile) []springConfigEntry {
	out := make([]springConfigEntry, 0, len(file.Lines))
	for i, line := range file.Lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "!") {
			continue
		}
		idx := strings.IndexAny(trimmed, "=:")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:idx])
		value := strings.TrimSpace(trimmed[idx+1:])
		if key == "" {
			continue
		}
		out = append(out, springConfigEntry{Key: key, RawValue: value, Line: i + 1})
	}
	return out
}

func parseYAMLEntries(file sourceFile) []springConfigEntry {
	out := make([]springConfigEntry, 0, 32)
	dec := yaml.NewDecoder(strings.NewReader(file.Text))
	for {
		var root yaml.Node
		if err := dec.Decode(&root); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return out
		}
		if len(root.Content) == 0 {
			continue
		}
		docEntries := make([]springConfigEntry, 0, 16)
		flattenYAMLNode(root.Content[0], "", &docEntries)
		profileOverride := springProfileFromEntries(docEntries)
		for i := range docEntries {
			docEntries[i].ProfileOverride = profileOverride
			out = append(out, docEntries[i])
		}
	}
	return out
}

func flattenYAMLNode(node *yaml.Node, prefix string, out *[]springConfigEntry) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			k := node.Content[i]
			v := node.Content[i+1]
			key := strings.TrimSpace(k.Value)
			if key == "" {
				continue
			}
			full := key
			if prefix != "" {
				full = prefix + "." + key
			}
			if v.Kind == yaml.MappingNode {
				flattenYAMLNode(v, full, out)
				continue
			}
			if v.Kind == yaml.SequenceNode {
				for idx, item := range v.Content {
					seqKey := full + "[" + strconvItoa(idx) + "]"
					flattenYAMLNode(item, seqKey, out)
				}
				continue
			}
			*out = append(*out, springConfigEntry{
				Key:      full,
				RawValue: strings.TrimSpace(v.Value),
				Line:     max(1, v.Line),
			})
		}
	case yaml.ScalarNode:
		key := prefix
		if key != "" {
			*out = append(*out, springConfigEntry{
				Key:      key,
				RawValue: strings.TrimSpace(node.Value),
				Line:     max(1, node.Line),
			})
		}
	}
}

func collectSpringValueRefs(files []sourceFile) []springValueRef {
	out := make([]springValueRef, 0, 16)
	for _, file := range files {
		for _, m := range regexMatchesByLine(file.Lines, reSpringValue) {
			if len(m.groups) == 0 {
				continue
			}
			key := springPlaceholderKey(m.groups[0])
			if key == "" {
				continue
			}
			out = append(out, springValueRef{
				Key:  key,
				File: file,
				Line: m.line,
				Col:  m.col,
				Text: m.text,
			})
		}
	}
	return out
}

func springPlaceholderKey(expr string) string {
	key := strings.TrimSpace(expr)
	if key == "" {
		return ""
	}
	if i := strings.Index(key, ":"); i >= 0 {
		key = strings.TrimSpace(key[:i])
	}
	return key
}

func resolveSpringPlaceholderValue(raw string) (resolved string, unresolved bool, placeholderVar string, placeholderDefault string, placeholderVars []string, placeholderDefaults []string, unresolvedVars []string) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false, "", "", nil, nil, nil
	}
	matches := reSpringPlaceholder.FindAllStringSubmatch(value, -1)
	if len(matches) == 0 {
		return value, false, "", "", nil, nil, nil
	}
	placeholderVars = make([]string, 0, len(matches))
	placeholderDefaults = make([]string, 0, len(matches))
	unresolvedVars = make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		varName := strings.TrimSpace(m[1])
		defaultValue := ""
		if len(m) >= 3 {
			defaultValue = strings.TrimSpace(m[2])
		}
		placeholderVars = append(placeholderVars, varName)
		placeholderDefaults = append(placeholderDefaults, defaultValue)
		if placeholderVar == "" {
			placeholderVar = varName
			placeholderDefault = defaultValue
		}
		replacement := ""
		if v := strings.TrimSpace(getEnv(varName)); v != "" {
			replacement = v
		} else if defaultValue != "" {
			replacement = defaultValue
		} else {
			unresolved = true
			unresolvedVars = append(unresolvedVars, varName)
			replacement = "<UNRESOLVED:" + varName + ">"
		}
		value = strings.Replace(value, m[0], replacement, 1)
	}
	return value, unresolved, placeholderVar, placeholderDefault, dedupeStrings(placeholderVars), placeholderDefaults, dedupeStrings(unresolvedVars)
}

func springPlaceholderStatus(unresolved bool, unresolvedCount int, total int) string {
	if total <= 0 {
		return "none"
	}
	if unresolved && unresolvedCount > 0 && unresolvedCount < total {
		return "partially_resolved"
	}
	if unresolved {
		return "unresolved"
	}
	return "resolved"
}

func springProfileFromEntries(entries []springConfigEntry) string {
	for _, e := range entries {
		key := strings.ToLower(strings.TrimSpace(e.Key))
		val := strings.TrimSpace(e.RawValue)
		if key == "spring.config.activate.on-profile" || key == "spring.profiles" || key == "spring.profiles.active" {
			v := strings.ToLower(strings.TrimSpace(val))
			if v == "" {
				continue
			}
			parts := strings.Split(v, ",")
			for _, p := range parts {
				pp := strings.TrimSpace(p)
				if pp != "" {
					return pp
				}
			}
		}
	}
	return ""
}

var getEnv = func(key string) string {
	return strings.TrimSpace(os.Getenv(strings.TrimSpace(key)))
}

func normalizeSpringProfile(profile string) string {
	p := strings.ToLower(strings.TrimSpace(profile))
	switch p {
	case "", "default":
		return "default"
	case "local":
		return "local"
	case "stage", "staging", "preprod", "preproduction":
		return "staging"
	case "prod", "production":
		return "prod"
	case "dev", "development":
		return "dev"
	case "test", "qa":
		return "test"
	default:
		return p
	}
}

func springLine(file sourceFile, line int) string {
	if line < 1 || line > len(file.Lines) {
		return ""
	}
	return file.Lines[line-1]
}

func hashString(v string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(v)))
	return hex.EncodeToString(h[:])
}

func sortedKeys[K ~string, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + (n % 10))
		n /= 10
	}
	return sign + string(buf[i:])
}
