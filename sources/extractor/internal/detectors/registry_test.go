package detectors

import "testing"

func TestRegistryHasStableKnownDetectors(t *testing.T) {
	for _, id := range []string{
		"golang.http.echo",
		"golang.http.fiber",
		"java.http.spring",
		"java.httpclient.feign",
		"java.httpclient.retrofit",
		"python.http.flask",
		"python.aws.sam",
	} {
		if !Exists(id) {
			t.Fatalf("missing detector %s", id)
		}
	}
}

func TestValidateIDRejectsUnknownDetector(t *testing.T) {
	if err := ValidateID("java.http.unknown"); err == nil {
		t.Fatal("expected unknown detector error")
	}
}
