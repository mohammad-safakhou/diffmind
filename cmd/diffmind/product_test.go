package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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
