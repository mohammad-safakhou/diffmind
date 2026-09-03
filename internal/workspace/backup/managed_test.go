package backup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/homelock"
)

func managedFixture(t *testing.T) (string, string) {
	t.Helper()
	home, _ := fixture(t)
	directory := filepath.Join(t.TempDir(), "snapshots")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	return home, directory
}

func TestManagedRotationRoundTripPreservesHistory(t *testing.T) {
	home, directory := managedFixture(t)
	historyPath := filepath.Join(home, "jobs/delivery-old.json")
	original, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatal(err)
	}
	var previous RotationReport
	for i := range 4 {
		result, err := Rotate(home, directory, "test", 2, DefaultMaxBytes)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Retained) != min(i+1, 2) || len(result.Removed) != min(max(i-1, 0), 1) {
			t.Fatalf("rotation %+v", result)
		}
		if i > 0 && result.Retained[1].ID != previous.Created.ID {
			t.Fatal("did not retain newest previous snapshot")
		}
		for _, id := range result.Removed {
			if _, err := os.Lstat(filepath.Join(directory, snapshotPrefix+id)); !os.IsNotExist(err) {
				t.Fatalf("expired snapshot remains: %v", err)
			}
		}
		previous = result
	}
	listed, err := ManagedList(home, directory, DefaultMaxBytes)
	if err != nil || !reflect.DeepEqual(listed, previous.Retained) {
		t.Fatalf("list %+v %v", listed, err)
	}
	after, err := os.ReadFile(historyPath)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("workspace history changed")
	}
	for _, file := range []string{catalogFile, filepath.Join(snapshotPrefix+previous.Created.ID, "receipt.json"), filepath.Join(snapshotPrefix+previous.Created.ID, "archive.tar.gz")} {
		info, err := os.Stat(filepath.Join(directory, file))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private file %s %v %v", file, info, err)
		}
	}
	if err := os.Rename(home, home+"-original"); err != nil {
		t.Fatal(err)
	}
	// Catalog inspection remains available during disaster recovery.
	if _, err := ManagedList(home, directory, DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(previous.Created.Archive, home, previous.Created.Report.SHA256, DefaultMaxBytes, false); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(historyPath)
	if err != nil || !bytes.Equal(original, restored) {
		t.Fatal("restore changed historical bytes/dates")
	}
}

func TestManagedRotationFailureNeverPrunes(t *testing.T) {
	for _, kind := range []string{"live-source", "busy-catalog", "oversize-source", "corrupt-archive", "corrupt-receipt", "foreign-receipt", "unknown-snapshot-file", "symlink-archive", "symlink-snapshot", "future-catalog", "missing-catalog", "retired-collision"} {
		t.Run(kind, func(t *testing.T) {
			home, directory := managedFixture(t)
			initial, err := Rotate(home, directory, "test", 1, DefaultMaxBytes)
			if err != nil {
				t.Fatal(err)
			}
			folder := filepath.Dir(initial.Created.Archive)
			limit := DefaultMaxBytes
			switch kind {
			case "live-source":
				release, err := homelock.Acquire(home, false)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			case "busy-catalog":
				release, err := homelock.Acquire(directory, true)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			case "oversize-source":
				limit = 4096
				if err := os.WriteFile(filepath.Join(home, "new-large-file"), []byte(strings.Repeat("a", 8192)), 0600); err != nil {
					t.Fatal(err)
				}
			case "corrupt-archive":
				if err := os.WriteFile(initial.Created.Archive, []byte("broken"), 0600); err != nil {
					t.Fatal(err)
				}
			case "corrupt-receipt":
				if err := os.WriteFile(filepath.Join(folder, "receipt.json"), []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			case "foreign-receipt":
				var receipt snapshotReceipt
				if err := readPrivateJSON(filepath.Join(folder, "receipt.json"), &receipt); err != nil {
					t.Fatal(err)
				}
				receipt.CatalogID = strings.Repeat("a", 32)
				body, _ := json.Marshal(receipt)
				if err := os.WriteFile(filepath.Join(folder, "receipt.json"), body, 0600); err != nil {
					t.Fatal(err)
				}
			case "unknown-snapshot-file":
				if err := os.WriteFile(filepath.Join(folder, "keep-me"), []byte("data"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink-archive":
				outside := filepath.Join(t.TempDir(), "archive")
				if err := os.Rename(initial.Created.Archive, outside); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, initial.Created.Archive); err != nil {
					t.Fatal(err)
				}
			case "symlink-snapshot":
				outside := filepath.Join(t.TempDir(), "snapshot")
				if err := os.Rename(folder, outside); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, folder); err != nil {
					t.Fatal(err)
				}
			case "future-catalog":
				if err := os.WriteFile(filepath.Join(directory, catalogFile), []byte(`{"format":999}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-catalog":
				if err := os.Remove(filepath.Join(directory, catalogFile)); err != nil {
					t.Fatal(err)
				}
			case "retired-collision":
				if err := os.Mkdir(filepath.Join(directory, ".retired-"+initial.Created.ID), 0700); err != nil {
					t.Fatal(err)
				}
			}
			result, err := Rotate(home, directory, "test", 1, limit)
			if err == nil || len(result.Removed) != 0 {
				t.Fatalf("unsafe rotation %+v %v", result, err)
			}
			if _, err := os.Lstat(folder); err != nil {
				t.Fatalf("old backup pruned after failure: %v", err)
			}
			if kind == "retired-collision" {
				if result.Created.ID == "" {
					t.Fatal("published snapshot not reported on partial retention failure")
				}
				if _, err := Verify(result.Created.Archive, result.Created.Report.SHA256, DefaultMaxBytes); err != nil {
					t.Fatal(err)
				}
			} else if result.Created.ID != "" {
				t.Fatalf("published despite failed preconditions: %+v", result)
			}
		})
	}
}

func TestManagedCatalogScopeAndRecoveryDebris(t *testing.T) {
	home, directory := managedFixture(t)
	for _, keep := range []int{-1, 0, 1001} {
		if _, err := Rotate(home, directory, "test", keep, DefaultMaxBytes); err == nil {
			t.Fatalf("keep %d accepted", keep)
		}
	}
	if _, err := Rotate(home, directory, "test", 1, 0); err == nil {
		t.Fatal("zero byte bound accepted")
	}
	if err := os.WriteFile(filepath.Join(directory, "unrelated"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Rotate(home, directory, "test", 1, DefaultMaxBytes); err == nil {
		t.Fatal("adopted nonempty directory")
	}
	if err := os.Remove(filepath.Join(directory, "unrelated")); err != nil {
		t.Fatal(err)
	}
	if _, err := Rotate(home, directory, "test", 1, DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"unrelated.tar.gz", ".pending-crashed", ".retired-crashed"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Rotate(home, directory, "test", 1, DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"unrelated.tar.gz", ".pending-crashed", ".retired-crashed"} {
		b, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(b) != "keep" {
			t.Fatalf("unowned file %s changed", name)
		}
	}
	other, _ := fixture(t)
	if _, err := Rotate(other, directory, "test", 1, DefaultMaxBytes); err == nil {
		t.Fatal("adopted foreign workspace")
	}
	if _, err := ManagedList(other, directory, DefaultMaxBytes); err == nil {
		t.Fatal("listed wrong workspace")
	}
	inside := filepath.Join(home, "backups")
	if err := os.Mkdir(inside, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Rotate(home, inside, "test", 1, DefaultMaxBytes); err == nil {
		t.Fatal("recursive backup accepted")
	}
	if _, err := Rotate(home, filepath.Dir(home), "test", 1, DefaultMaxBytes); err == nil {
		t.Fatal("ancestor catalog accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(directory, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Rotate(home, link, "test", 1, DefaultMaxBytes); err == nil {
		t.Fatal("symlink catalog accepted")
	}
	if err := os.Chmod(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Rotate(home, directory, "test", 1, DefaultMaxBytes); err == nil {
		t.Fatal("public catalog accepted")
	}
}

func TestManagedConcurrentRotators(t *testing.T) {
	home, directory := managedFixture(t)
	if _, err := Rotate(home, directory, "test", 2, DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Rotate(home, directory, "test", 2, DefaultMaxBytes)
			if err != nil && !strings.Contains(err.Error(), "catalog busy") {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	items, err := ManagedList(home, directory, DefaultMaxBytes)
	if err != nil || len(items) != 2 {
		t.Fatalf("concurrent rotation %+v %v", items, err)
	}
}
