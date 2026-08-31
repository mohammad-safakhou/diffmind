package artifacts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ReadDiffMindFileMaps returns raw entity maps grouped by type for the rich
// architecture graph endpoint. Protocol is preferred; legacy run-directory JSON is
// kept only for already-generated run directories.
func ReadDiffMindFileMaps(path string) (map[string][]map[string]any, map[string][]map[string]any, map[string][]map[string]any) {
	if exp, dep, conn, ok := protocolFileMaps(path); ok {
		return exp, dep, conn
	}
	return readRunDirMaps(path)
}

func readRunDirMaps(path string) (map[string][]map[string]any, map[string][]map[string]any, map[string][]map[string]any) {
	return readJSONDirMaps(filepath.Join(path, "exposures")),
		readJSONDirMaps(filepath.Join(path, "dependencies")),
		readJSONDirMaps(filepath.Join(path, "connections"))
}

func readJSONDirMaps(dir string) map[string][]map[string]any {
	out := make(map[string][]map[string]any)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var items []map[string]any
		if json.Unmarshal(b, &items) == nil {
			out[strings.TrimSuffix(e.Name(), ".json")] = items
		}
	}
	return out
}
