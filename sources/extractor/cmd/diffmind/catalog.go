package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mohammad-safakhou/diffmind/internal/archfile"
	"github.com/mohammad-safakhou/diffmind/internal/catalog"
)

// generatedName is the transient proposal file DiffMind writes next to the
// human-authored discovery file. merge-file folds it into the main file.
const generatedName = ".diffmind.generated.yaml"

func catalogCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: diffmind catalog <import-file|export-file|merge-file> <diffmind.yaml> [--out dir]")
		os.Exit(2)
	}
	switch args[0] {
	case "import-file":
		catalogImportFile(args[1:])
	case "export-file":
		catalogExportFile(args[1:])
	case "merge-file":
		catalogMergeFile(args[1:])
	default:
		fmt.Fprintln(os.Stderr, "unknown catalog command:", args[0])
		os.Exit(2)
	}
}

func catalogImportFile(args []string) {
	fs := flag.NewFlagSet("catalog import-file", flag.ExitOnError)
	baseDir := fs.String("out", "", "catalog base directory (default ~/.diffmind/runs)")
	fs.Parse(args)
	path := requireFileArg(fs)

	resolved, err := archfile.Resolve(path)
	if err != nil {
		fail("resolve", err)
	}
	in, err := archfile.ToModel(resolved, "file:"+filepath.Base(path))
	if err != nil {
		fail("map", err)
	}
	store := catalog.NewStore(resolveBaseDir(*baseDir))
	_, sum, err := store.ImportManual(in)
	if err != nil {
		fail("import", err)
	}
	fmt.Printf("imported %s: %d added, %d updated\n", path, sum.Added, sum.Updated)
}

func catalogExportFile(args []string) {
	fs := flag.NewFlagSet("catalog export-file", flag.ExitOnError)
	baseDir := fs.String("out", "", "catalog base directory (default ~/.diffmind/runs)")
	fs.Parse(args)
	path := requireFileArg(fs)

	store := catalog.NewStore(resolveBaseDir(*baseDir))
	doc, err := store.Load()
	if err != nil {
		fail("load catalog", err)
	}
	genPath := filepath.Join(filepath.Dir(path), generatedName)
	n, err := archfile.WriteGenerated(doc, path, genPath)
	if err != nil {
		fail("export", err)
	}
	if n == 0 {
		fmt.Printf("nothing new to propose (every automation fact is already in %s)\n", filepath.Base(path))
		return
	}
	fmt.Printf("proposed %d new record(s) in %s — review, then `diffmind catalog merge-file %s`\n", n, genPath, path)
}

func catalogMergeFile(args []string) {
	fs := flag.NewFlagSet("catalog merge-file", flag.ExitOnError)
	fs.Parse(args)
	path := requireFileArg(fs)

	genPath := filepath.Join(filepath.Dir(path), generatedName)
	n, err := archfile.MergeIntoMain(path, genPath)
	if err != nil {
		fail("merge", err)
	}
	fmt.Printf("merged %d new record(s) into %s\n", n, path)
}

func requireFileArg(fs *flag.FlagSet) string {
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "a path to the discovery file (diffmind.yaml) is required")
		os.Exit(2)
	}
	return fs.Arg(0)
}

func fail(stage string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", stage, err)
	os.Exit(1)
}
