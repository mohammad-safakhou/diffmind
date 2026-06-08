package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// LoadRunArtifacts reads the per-type JSON arrays a finished run wrote under
// <runDir>/{exposures,dependencies,connections}/ into an Extracted value, so
// the scorer can grade a real run (e.g. one produced by `diffmind run`) against
// a label without re-running anything.
func LoadRunArtifacts(runDir string) (Extracted, error) {
	var ext Extracted
	expRaw, err := readJSONArrays(filepath.Join(runDir, "exposures"))
	if err != nil {
		return ext, err
	}
	for _, b := range expRaw {
		var e model.Exposure
		if err := json.Unmarshal(b, &e); err == nil && e.Type != "" {
			ext.Exposures = append(ext.Exposures, e)
		}
	}
	depRaw, err := readJSONArrays(filepath.Join(runDir, "dependencies"))
	if err != nil {
		return ext, err
	}
	for _, b := range depRaw {
		var d model.Dependency
		if err := json.Unmarshal(b, &d); err == nil && d.Type != "" {
			ext.Dependencies = append(ext.Dependencies, d)
		}
	}
	connRaw, err := readJSONArrays(filepath.Join(runDir, "connections"))
	if err != nil {
		return ext, err
	}
	for _, b := range connRaw {
		var c model.Connection
		if err := json.Unmarshal(b, &c); err == nil && c.ID != "" {
			ext.Connections = append(ext.Connections, c)
		}
	}
	return ext, nil
}

// readJSONArrays reads every *.json file in dir, treating each as a JSON array,
// and returns the flattened element-level raw messages. A missing directory is
// not an error (a run may legitimately have zero of a kind).
func readJSONArrays(dir string) ([]json.RawMessage, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var out []json.RawMessage
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		var arr []json.RawMessage
		if err := json.Unmarshal(b, &arr); err != nil {
			// Tolerate a single-object file too.
			var obj json.RawMessage
			if json.Unmarshal(b, &obj) == nil {
				out = append(out, obj)
			}
			continue
		}
		out = append(out, arr...)
	}
	return out, nil
}
