package knowledge

import (
	"fmt"
	"strconv"
	"strings"
)

// RuntimeVersion is the knowledge-pack API version implemented by this
// release. It is intentionally separate from a marketing/build version.
const RuntimeVersion = "0.1.0"

type semanticVersion struct{ major, minor, patch int }

func parseSemanticVersion(raw string) (semanticVersion, error) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "v")
	if index := strings.IndexAny(raw, "-+"); index >= 0 {
		raw = raw[:index]
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return semanticVersion{}, fmt.Errorf("expected major.minor.patch")
	}
	values := [3]int{}
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return semanticVersion{}, fmt.Errorf("invalid semantic version component %q", part)
		}
		values[i] = value
	}
	return semanticVersion{values[0], values[1], values[2]}, nil
}

func compareVersion(left, right semanticVersion) int {
	if left.major != right.major {
		if left.major < right.major {
			return -1
		}
		return 1
	}
	if left.minor != right.minor {
		if left.minor < right.minor {
			return -1
		}
		return 1
	}
	if left.patch < right.patch {
		return -1
	}
	if left.patch > right.patch {
		return 1
	}
	return 0
}

// Compatible reports whether a runtime version satisfies a whitespace- or
// comma-separated conjunction such as ">=0.1.0 <2.0.0".
func Compatible(constraint, runtime string) (bool, error) {
	current, err := parseSemanticVersion(runtime)
	if err != nil {
		return false, err
	}
	constraint = strings.ReplaceAll(strings.TrimSpace(constraint), ",", " ")
	if constraint == "*" {
		return true, nil
	}
	terms := strings.Fields(constraint)
	if len(terms) == 0 {
		return false, fmt.Errorf("compatibility constraint is empty")
	}
	for _, term := range terms {
		operator := "="
		versionText := term
		for _, candidate := range []string{">=", "<=", ">", "<", "="} {
			if strings.HasPrefix(term, candidate) {
				operator = candidate
				versionText = strings.TrimPrefix(term, candidate)
				break
			}
		}
		required, err := parseSemanticVersion(versionText)
		if err != nil {
			return false, fmt.Errorf("invalid compatibility term %q: %w", term, err)
		}
		comparison := compareVersion(current, required)
		satisfied := map[string]bool{
			"=": comparison == 0, ">=": comparison >= 0, "<=": comparison <= 0,
			">": comparison > 0, "<": comparison < 0,
		}[operator]
		if !satisfied {
			return false, nil
		}
	}
	return true, nil
}
