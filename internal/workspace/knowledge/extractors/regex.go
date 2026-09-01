package extractors

import (
	"fmt"
	"os"
	"regexp"
)

// ExtractRegex reads a file and applies a regex pattern, returning all
// matches of the first capture group (or the full match if no group).
func ExtractRegex(filePath, pattern string) ([]string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", filePath, err)
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile regex %q: %w", pattern, err)
	}

	allMatches := re.FindAllStringSubmatch(string(data), -1)
	var results []string
	for _, match := range allMatches {
		if len(match) > 1 {
			results = append(results, match[1]) // first capture group
		} else {
			results = append(results, match[0]) // full match
		}
	}
	return results, nil
}
