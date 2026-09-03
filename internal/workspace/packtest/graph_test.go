package packtest

import (
	"path/filepath"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/knowledge"
)

func TestOfficialPacks(t *testing.T) {
	packs, err := knowledge.LoadPacksFromDirs([]string{filepath.Join("..", "..", "..", "packs")})
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) < 3 {
		t.Fatal("missing official pack fixtures")
	}
	for _, pack := range packs {
		t.Run(pack.ID, func(t *testing.T) {
			for _, result := range RunTests(pack) {
				if !result.Passed {
					t.Errorf("%s: %s", result.Name, result.Error)
				}
			}
		})
	}
}

func TestGraphFixturesRejectMissingExtraAndIncorrectEdges(t *testing.T) {
	pack, err := knowledge.LoadPack(filepath.Join("..", "..", "..", "packs", "service-manifest", "pack.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing", "extra", "wrong direction", "unknown fixture", "symlink escape"} {
		t.Run(mode, func(t *testing.T) {
			test := pack.GraphTests[0]
			test.Edges = append([]knowledge.ExpectedEdge(nil), test.Edges...)
			test.Repositories = append([]knowledge.GraphTestRepository(nil), test.Repositories...)
			switch mode {
			case "missing":
				test.Edges = test.Edges[1:]
			case "extra":
				test.Edges = append(test.Edges, knowledge.ExpectedEdge{From: "catalog", To: "billing", Type: "http"})
			case "wrong direction":
				test.Edges[0].From, test.Edges[0].To = test.Edges[0].To, test.Edges[0].From
			case "unknown fixture":
				test.Repositories[0].Fixture = "missing"
			case "symlink escape":
				test.Repositories[0].Fixture = "../outside"
			}
			if err := runGraphTest(pack, test); err == nil {
				t.Fatal("broken graph fixture passed")
			}
		})
	}
}
