package blueprints

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ValidationError is a single structured problem with a blueprint body. Field
// is a dotted path into the document (e.g. "extractions[0].strategy") so the
// in-UI editor can point the user at the exact issue.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// knownStrategies are the extraction strategies the engine understands.
var knownStrategies = map[string]bool{"": true, "field_path": true, "regex": true}

// knownKinds are the applies_to kinds the matcher understands.
var knownKinds = map[string]bool{"service_repo": true, "infra_repo": true, "any": true, "": true}

// ValidateBlueprint parses and structurally validates a blueprint body. It
// returns (parsed, nil) when valid, or (nil, errors) with one entry per
// problem. A JSON syntax error is reported as a single error on the root.
func ValidateBlueprint(body []byte) (*Blueprint, []ValidationError) {
	var bp Blueprint
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&bp); err != nil {
		// Fall back to a lenient parse so we can still report field-level
		// problems for unknown-field errors rather than only syntax.
		if err2 := json.Unmarshal(body, &bp); err2 != nil {
			return nil, []ValidationError{{Field: "", Message: "invalid JSON: " + err2.Error()}}
		}
		// Unknown fields are a soft warning surfaced as a field error but we
		// continue structural validation against the lenient parse.
		errs := validateStructure(&bp)
		errs = append(errs, ValidationError{Field: "", Message: "unknown field present: " + strings.TrimPrefix(err.Error(), "json: ")})
		return nil, errs
	}
	if errs := validateStructure(&bp); len(errs) > 0 {
		return nil, errs
	}
	return &bp, nil
}

func validateStructure(bp *Blueprint) []ValidationError {
	var errs []ValidationError
	if strings.TrimSpace(bp.Name) == "" {
		errs = append(errs, ValidationError{Field: "name", Message: "name is required"})
	}
	if !knownKinds[bp.AppliesTo.Kind] {
		errs = append(errs, ValidationError{Field: "applies_to.kind", Message: fmt.Sprintf("unknown kind %q (want service_repo, infra_repo, or any)", bp.AppliesTo.Kind)})
	}
	if len(bp.Extractions) == 0 {
		errs = append(errs, ValidationError{Field: "extractions", Message: "at least one extraction is required"})
	}
	for i, ex := range bp.Extractions {
		base := fmt.Sprintf("extractions[%d]", i)
		if strings.TrimSpace(ex.Name) == "" {
			errs = append(errs, ValidationError{Field: base + ".name", Message: "extraction name is required"})
		}
		if !knownStrategies[ex.Strategy] {
			errs = append(errs, ValidationError{Field: base + ".strategy", Message: fmt.Sprintf("unknown strategy %q (want field_path or regex)", ex.Strategy)})
		}
		if strings.TrimSpace(ex.Source.Glob) == "" {
			errs = append(errs, ValidationError{Field: base + ".source.glob", Message: "source.glob is required"})
		}
		if len(ex.Extract) == 0 {
			errs = append(errs, ValidationError{Field: base + ".extract", Message: "at least one extract field is required"})
		}
		for j, f := range ex.Extract {
			fb := fmt.Sprintf("%s.extract[%d]", base, j)
			if strings.TrimSpace(f.MapsTo) == "" {
				errs = append(errs, ValidationError{Field: fb + ".maps_to", Message: "maps_to is required"})
			}
			if ex.Strategy == "regex" && strings.TrimSpace(f.Pattern) == "" {
				errs = append(errs, ValidationError{Field: fb + ".pattern", Message: "regex strategy requires a pattern"})
			}
			if (ex.Strategy == "" || ex.Strategy == "field_path") && strings.TrimSpace(f.Field) == "" {
				errs = append(errs, ValidationError{Field: fb + ".field", Message: "field_path strategy requires a field"})
			}
		}
	}
	return errs
}
