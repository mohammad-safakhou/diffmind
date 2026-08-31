package ast

import "testing"

func TestSortFrameworkBindingsStable(t *testing.T) {
	bindings := []FrameworkBinding{
		{Framework: "spring", Kind: "queue_consumer", File: "z.java", Symbol: "Z.handle"},
		{Framework: "spring", Kind: "http_handler", File: "b.java", Range: Range{StartLine: 20}, Symbol: "B.get"},
		{Framework: "spring", Kind: "http_handler", File: "a.java", Range: Range{StartLine: 10}, Symbol: "A.get"},
	}

	sortFrameworkBindings(bindings)

	got := []string{bindings[0].Symbol, bindings[1].Symbol, bindings[2].Symbol}
	want := []string{"A.get", "B.get", "Z.handle"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
