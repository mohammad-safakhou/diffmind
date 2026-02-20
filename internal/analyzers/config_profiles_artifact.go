package analyzers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"diffmind/internal/facts"
)

type resolvedConfigProfilesArtifact struct {
	SnapshotID string                                 `json:"snapshot_id"`
	SourceRoot string                                 `json:"source_root"`
	Profiles   map[string]resolvedConfigProfileBucket `json:"profiles"`
}

type resolvedConfigProfileBucket struct {
	Resolved map[string]resolvedConfigValue `json:"resolved"`
	CodeRefs map[string]resolvedCodeRef     `json:"code_refs"`
}

type resolvedConfigValue struct {
	OriginFile            string `json:"origin_file,omitempty"`
	ResolvedValueHash     string `json:"resolved_value_hash,omitempty"`
	PlaceholderStatus     string `json:"placeholder_status,omitempty"`
	PlaceholderVars       string `json:"placeholder_vars,omitempty"`
	PlaceholderUnresolved string `json:"placeholder_unresolved_vars,omitempty"`
}

type resolvedCodeRef struct {
	OriginFile            string `json:"origin_file,omitempty"`
	RefFile               string `json:"ref_file,omitempty"`
	ResolvedValueHash     string `json:"resolved_value_hash,omitempty"`
	PlaceholderStatus     string `json:"placeholder_status,omitempty"`
	PlaceholderVars       string `json:"placeholder_vars,omitempty"`
	PlaceholderUnresolved string `json:"placeholder_unresolved_vars,omitempty"`
}

func writeResolvedConfigProfilesArtifact(outDir string, report *Report, bundle facts.Bundle) (string, string, bool, error) {
	if report == nil {
		return "", "", false, nil
	}
	artifact, ok := buildResolvedConfigProfilesArtifact(*report, bundle)
	if !ok {
		return "", "", false, nil
	}
	path := filepath.Join(outDir, "analyzers", "resolved_config_profiles.json")
	if err := writeJSON(path, artifact); err != nil {
		return "", "", false, err
	}
	return path, hashResolvedConfigArtifact(artifact), true, nil
}

func buildResolvedConfigProfilesArtifact(report Report, bundle facts.Bundle) (resolvedConfigProfilesArtifact, bool) {
	out := resolvedConfigProfilesArtifact{
		SnapshotID: report.SnapshotID,
		SourceRoot: report.SourceRoot,
		Profiles:   map[string]resolvedConfigProfileBucket{},
	}
	hasData := false
	for _, f := range bundle.Facts {
		if !strings.EqualFold(strings.TrimSpace(f.Type), "ConfigKey") {
			continue
		}
		attrs := f.Attributes
		pattern, _ := attrs["pattern"].(string)
		profile, _ := attrs["profile"].(string)
		key, _ := attrs["key"].(string)
		if strings.TrimSpace(profile) == "" || strings.TrimSpace(key) == "" {
			continue
		}
		pattern = strings.TrimSpace(pattern)
		if pattern != "spring_profile_resolved" && pattern != "spring_code_ref_resolved" {
			continue
		}
		bucket := out.Profiles[profile]
		if bucket.Resolved == nil {
			bucket.Resolved = map[string]resolvedConfigValue{}
		}
		if bucket.CodeRefs == nil {
			bucket.CodeRefs = map[string]resolvedCodeRef{}
		}
		if pattern == "spring_profile_resolved" {
			bucket.Resolved[key] = resolvedConfigValue{
				OriginFile:            stringAttr(attrs, "origin_file"),
				ResolvedValueHash:     stringAttr(attrs, "resolved_value_hash"),
				PlaceholderStatus:     stringAttr(attrs, "placeholder_status"),
				PlaceholderVars:       stringAttr(attrs, "placeholder_vars"),
				PlaceholderUnresolved: stringAttr(attrs, "placeholder_unresolved_vars"),
			}
			hasData = true
		} else {
			refKey := key + "|" + stringAttr(attrs, "ref_file")
			bucket.CodeRefs[refKey] = resolvedCodeRef{
				OriginFile:            stringAttr(attrs, "origin_file"),
				RefFile:               stringAttr(attrs, "ref_file"),
				ResolvedValueHash:     stringAttr(attrs, "resolved_value_hash"),
				PlaceholderStatus:     stringAttr(attrs, "placeholder_status"),
				PlaceholderVars:       stringAttr(attrs, "placeholder_vars"),
				PlaceholderUnresolved: stringAttr(attrs, "placeholder_unresolved_vars"),
			}
			hasData = true
		}
		out.Profiles[profile] = bucket
	}
	return out, hasData
}

func hashResolvedConfigArtifact(a resolvedConfigProfilesArtifact) string {
	type hashCodeRef struct {
		Key                   string `json:"key"`
		OriginFile            string `json:"origin_file,omitempty"`
		RefFile               string `json:"ref_file,omitempty"`
		ResolvedValueHash     string `json:"resolved_value_hash,omitempty"`
		PlaceholderStatus     string `json:"placeholder_status,omitempty"`
		PlaceholderVars       string `json:"placeholder_vars,omitempty"`
		PlaceholderUnresolved string `json:"placeholder_unresolved_vars,omitempty"`
	}
	type hashResolved struct {
		Key                   string `json:"key"`
		OriginFile            string `json:"origin_file,omitempty"`
		ResolvedValueHash     string `json:"resolved_value_hash,omitempty"`
		PlaceholderStatus     string `json:"placeholder_status,omitempty"`
		PlaceholderVars       string `json:"placeholder_vars,omitempty"`
		PlaceholderUnresolved string `json:"placeholder_unresolved_vars,omitempty"`
	}
	type hashProfile struct {
		Profile  string         `json:"profile"`
		Resolved []hashResolved `json:"resolved"`
		CodeRefs []hashCodeRef  `json:"code_refs"`
	}
	payload := struct {
		SnapshotID string        `json:"snapshot_id"`
		SourceRoot string        `json:"source_root"`
		Profiles   []hashProfile `json:"profiles"`
	}{
		SnapshotID: strings.TrimSpace(a.SnapshotID),
		SourceRoot: strings.TrimSpace(a.SourceRoot),
	}
	names := make([]string, 0, len(a.Profiles))
	for profile := range a.Profiles {
		names = append(names, profile)
	}
	sort.Strings(names)
	for _, profile := range names {
		b := a.Profiles[profile]
		p := hashProfile{Profile: profile}
		resolvedKeys := make([]string, 0, len(b.Resolved))
		for k := range b.Resolved {
			resolvedKeys = append(resolvedKeys, k)
		}
		sort.Strings(resolvedKeys)
		for _, k := range resolvedKeys {
			v := b.Resolved[k]
			p.Resolved = append(p.Resolved, hashResolved{
				Key:                   k,
				OriginFile:            v.OriginFile,
				ResolvedValueHash:     v.ResolvedValueHash,
				PlaceholderStatus:     v.PlaceholderStatus,
				PlaceholderVars:       v.PlaceholderVars,
				PlaceholderUnresolved: v.PlaceholderUnresolved,
			})
		}
		codeRefKeys := make([]string, 0, len(b.CodeRefs))
		for k := range b.CodeRefs {
			codeRefKeys = append(codeRefKeys, k)
		}
		sort.Strings(codeRefKeys)
		for _, k := range codeRefKeys {
			v := b.CodeRefs[k]
			p.CodeRefs = append(p.CodeRefs, hashCodeRef{
				Key:                   k,
				OriginFile:            v.OriginFile,
				RefFile:               v.RefFile,
				ResolvedValueHash:     v.ResolvedValueHash,
				PlaceholderStatus:     v.PlaceholderStatus,
				PlaceholderVars:       v.PlaceholderVars,
				PlaceholderUnresolved: v.PlaceholderUnresolved,
			})
		}
		payload.Profiles = append(payload.Profiles, p)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func stringAttr(attrs map[string]any, key string) string {
	if attrs == nil {
		return ""
	}
	v, _ := attrs[key].(string)
	return strings.TrimSpace(v)
}
