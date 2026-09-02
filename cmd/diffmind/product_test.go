package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestVersionJSON(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	version, commit, date = "1.2.3", "abc123", "2026-09-01T00:00:00Z"
	t.Cleanup(func() { version, commit, date = oldVersion, oldCommit, oldDate })
	var out bytes.Buffer
	if err := runVersion([]string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var got versionInfo
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != "1.2.3" || got.Commit != "abc123" || got.OS == "" || got.Arch == "" {
		t.Fatalf("version=%+v", got)
	}
}

func TestDoctorFreshInstall(t *testing.T) {
	t.Setenv("DIFFMIND_HOME", t.TempDir())
	var out bytes.Buffer
	code, err := runDoctor([]string{"--json"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if code != 0 || !report.OK {
		t.Fatalf("code=%d report=%+v", code, report)
	}
	joined := out.String()
	if !strings.Contains(joined, "knowledge_packs") || !strings.Contains(joined, "projects") {
		t.Fatalf("report=%s", joined)
	}
}

func TestDoctorRejectsExtraArguments(t *testing.T) {
	if _, err := runDoctor([]string{"unexpected"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected usage error")
	}
}

func TestValidateUIExposure(t *testing.T) {
	for _, host := range []string{"", "localhost", "127.0.0.1", "::1", "[::1]"} {
		if err := validateUIExposure(host, "", false); err != nil {
			t.Errorf("loopback host %q rejected: %v", host, err)
		}
	}
	if err := validateUIExposure("0.0.0.0", "", false); err == nil {
		t.Fatal("expected unauthenticated public bind to be rejected")
	}
	if err := validateUIExposure("0.0.0.0", "token", false); err != nil {
		t.Fatalf("authenticated public bind rejected: %v", err)
	}
	if err := validateUIExposure("10.0.0.2", "", true); err != nil {
		t.Fatalf("explicit unauthenticated bind rejected: %v", err)
	}
}

func TestSharedServerEnvironmentParsing(t *testing.T) {
	t.Setenv("DIFFMIND_REFRESH_ON_START", "true")
	t.Setenv("DIFFMIND_REFRESH_CONCURRENCY", "7")
	if !envBool("DIFFMIND_REFRESH_ON_START") {
		t.Fatal("expected true boolean environment value")
	}
	if got := envInt("DIFFMIND_REFRESH_CONCURRENCY", 4); got != 7 {
		t.Fatalf("concurrency = %d, want 7", got)
	}
	if got, err := parseOptionalDuration("15m"); err != nil || got != 15*time.Minute {
		t.Fatalf("duration = %v, %v", got, err)
	}
	if _, err := parseOptionalDuration("tomorrow"); err == nil {
		t.Fatal("expected invalid duration to fail")
	}
}
