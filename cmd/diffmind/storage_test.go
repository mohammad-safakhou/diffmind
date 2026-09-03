package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/homelock"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func TestStorageCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIFFMIND_HOME", home)
	for _, args := range [][]string{{"migrate"}, {"verify"}, {"unknown"}, {"migrate", "--offline", "extra"}, {"verify", "--offline", "--bad"}} {
		if err := runStorage(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	for _, args := range [][]string{nil, {"help"}, {"migrate", "--help"}} {
		var out bytes.Buffer
		if err := runStorage(args, &out); err != nil || out.Len() == 0 {
			t.Fatalf("help %v %v", args, err)
		}
	}
	st, err := store.New(home)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(store.Project{Name: "command-test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.EnqueueJob(p.ID, "manual", "", "", 4); err != nil {
		t.Fatal(err)
	}
	for i, command := range []string{"verify", "migrate", "migrate", "verify"} {
		var out bytes.Buffer
		if err := runStorage([]string{command, "--offline", "--json"}, &out); err != nil {
			t.Fatal(err)
		}
		var r store.QueueReport
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		want := "sqlite"
		if i == 0 {
			want = "json"
		}
		if r.Backend != want || r.Jobs != 1 {
			t.Fatalf("report %+v", r)
		}
	}
	release, err := homelock.Acquire(home, false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, command := range []string{"verify", "migrate"} {
		if err := runStorage([]string{command, "--offline"}, &bytes.Buffer{}); err == nil {
			t.Fatal("maintenance lease bypassed")
		}
	}
}
