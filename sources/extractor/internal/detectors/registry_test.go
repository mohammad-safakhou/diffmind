package detectors

import "testing"

func TestRegistryHasStableKnownDetectors(t *testing.T) {
	for _, id := range []string{
		"golang.http.echo",
		"golang.http.fiber",
		"golang.db.bun",
		"java.http.spring",
		"java.httpclient.feign",
		"java.httpclient.retrofit",
		"javascript.http.express",
		"typescript.http.nestjs",
		"python.http.flask",
		"python.http.fastapi",
		"python.http.django",
		"python.aws.sam",
		"ruby.http.rails",
		"php.http.laravel",
		"dotnet.http.aspnet",
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
