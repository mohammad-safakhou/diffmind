package detectors

import "fmt"

func ValidateID(id string) error {
	if Exists(id) {
		return nil
	}
	return fmt.Errorf("unknown detector %q", id)
}
