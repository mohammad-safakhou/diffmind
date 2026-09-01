package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/config"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/knowledge"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
)

const packUsage = `Usage:
  diffmind pack init <directory> [--id <id>]
  diffmind pack lint <file-or-directory>
  diffmind pack test <file-or-directory>
  diffmind pack explain <pack> --repo <repository> [--kind service_repo]
  diffmind pack install <directory-or-git-url> [--ref <git-ref>]
  diffmind pack enable <id>
  diffmind pack disable <id>
  diffmind pack list
`

func cmdPack(args []string) {
	if err := runPack(args, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "pack:", err)
		os.Exit(1)
	}
}

func runPack(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, packUsage)
		return errors.New("subcommand is required")
	}
	switch args[0] {
	case "init":
		return packInit(args[1:], stdout)
	case "lint":
		return packLint(args[1:], stdout)
	case "test":
		return packTest(args[1:], stdout)
	case "explain":
		return packExplain(args[1:], stdout)
	case "install":
		return packInstall(args[1:], stdout)
	case "enable", "disable":
		if len(args) != 2 {
			return fmt.Errorf("usage: diffmind pack %s <id>", args[0])
		}
		enabled := args[0] == "enable"
		if err := knowledge.SetEnabled(config.Home(), args[1], enabled); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s\t%s\n", args[1], map[bool]string{true: "enabled", false: "disabled"}[enabled])
		return nil
	case "list":
		if len(args) != 1 {
			return errors.New("usage: diffmind pack list")
		}
		lock, err := knowledge.ReadLock(config.Home())
		if err != nil {
			return err
		}
		for _, pack := range lock.Packs {
			fmt.Fprintf(stdout, "%s\t%s\t%t\t%s\t%s\n", pack.ID, pack.Version, pack.Enabled, pack.Revision, pack.Digest)
		}
		return nil
	case "help", "--help", "-h":
		fmt.Fprint(stdout, packUsage)
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q\n%s", args[0], packUsage)
	}
}

func packInit(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("pack init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	id := flags.String("id", "", "pack id")
	if err := flags.Parse(leadingPositionalLast(args)); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: diffmind pack init <directory> [--id <id>]")
	}
	dir := flags.Arg(0)
	if *id == "" {
		*id = strings.ToLower(strings.ReplaceAll(filepath.Base(filepath.Clean(dir)), "_", "-"))
	}
	manifest := filepath.Join(dir, "pack.yaml")
	if _, err := os.Stat(manifest); err == nil {
		return fmt.Errorf("%s already exists", manifest)
	} else if !os.IsNotExist(err) {
		return err
	}
	pack := &knowledge.Pack{
		APIVersion: knowledge.APIVersion, Kind: knowledge.Kind, ID: *id,
		Name: *id, Description: "Describe the repository conventions this pack teaches DiffMind.",
		Version: "0.1.0", License: "Apache-2.0", Compatibility: ">=0.1.0",
		AppliesTo: knowledge.AppliesTo{Kind: "service_repo", Match: knowledge.MatchConfig{HasFile: "service.yaml"}},
		Extractions: []knowledge.Extraction{{
			Name: "service identity", Source: knowledge.ExtractionSource{Glob: "service.yaml"},
			Strategy: "field_path", Extract: []knowledge.ExtractField{{Field: "name", MapsTo: "service_name"}},
		}},
		Tests: []knowledge.TestCase{{
			Name: "extracts service name", Fixture: "testdata/basic", RepoKind: "service_repo",
			Expected: knowledge.ExpectedIdentity{ServiceName: "example-service"},
		}},
	}
	body, err := knowledge.MarshalYAML(pack)
	if err != nil {
		return err
	}
	if _, validation := knowledge.ValidatePack(body, ".yaml"); len(validation) > 0 {
		return errors.New(knowledge.FormatValidationErrors(validation))
	}
	if err := os.MkdirAll(filepath.Join(dir, "testdata", "basic"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(manifest, body, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "testdata", "basic", "service.yaml"), []byte("name: example-service\n"), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(stdout, manifest)
	return nil
}

func packLint(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: diffmind pack lint <file-or-directory>")
	}
	packs, err := loadPackArgument(args[0])
	if err != nil {
		return err
	}
	if len(packs) == 0 {
		return errors.New("no pack manifests found")
	}
	for _, pack := range packs {
		fmt.Fprintf(stdout, "ok\t%s@%s\n", pack.ID, pack.Version)
	}
	return nil
}

func packTest(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: diffmind pack test <file-or-directory>")
	}
	packs, err := loadPackArgument(args[0])
	if err != nil {
		return err
	}
	if len(packs) == 0 {
		return errors.New("no pack manifests found")
	}
	var failures []string
	for _, pack := range packs {
		if len(pack.Tests) == 0 {
			failures = append(failures, pack.ID+": no tests declared")
			continue
		}
		for _, result := range knowledge.RunTests(pack) {
			if result.Passed {
				fmt.Fprintf(stdout, "ok\t%s\t%s\n", pack.ID, result.Name)
			} else {
				fmt.Fprintf(stdout, "FAIL\t%s\t%s\t%s\n", pack.ID, result.Name, result.Error)
				failures = append(failures, pack.ID+"/"+result.Name)
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d pack tests failed: %s", len(failures), strings.Join(failures, ", "))
	}
	return nil
}

func packExplain(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("pack explain", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	repo := flags.String("repo", "", "repository path")
	kind := flags.String("kind", "service_repo", "repository kind")
	if err := flags.Parse(leadingPositionalLast(args)); err != nil {
		return err
	}
	if flags.NArg() != 1 || *repo == "" {
		return errors.New("usage: diffmind pack explain <pack> --repo <repository> [--kind service_repo]")
	}
	packs, err := loadPackArgument(flags.Arg(0))
	if err != nil {
		return err
	}
	type explanation struct {
		Pack     string                       `json:"pack"`
		Matched  bool                         `json:"matched"`
		Evidence []knowledge.ExtractionResult `json:"evidence,omitempty"`
		Identity any                          `json:"identity,omitempty"`
	}
	var output []explanation
	engine := knowledge.NewEngine(util.NewLogger(util.LevelInfo))
	for _, pack := range packs {
		item := explanation{Pack: pack.ID + "@" + pack.Version, Matched: knowledge.Matches(pack, *repo, *kind)}
		if item.Matched {
			item.Evidence = engine.Run(pack, *repo)
			identity, err := knowledge.ToIdentity(filepath.Base(*repo), *repo, item.Evidence)
			if err != nil {
				return err
			}
			item.Identity = identity
		}
		output = append(output, item)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}

func packInstall(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("pack install", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	ref := flags.String("ref", "", "git ref")
	if err := flags.Parse(leadingPositionalLast(args)); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: diffmind pack install <directory-or-git-url> [--ref <git-ref>]")
	}
	locked, err := knowledge.Install(knowledge.InstallOptions{Home: config.Home(), Source: flags.Arg(0), Ref: *ref})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s@%s\t%s\n", locked.ID, locked.Version, locked.Digest)
	return nil
}

// Go's flag package stops at the first positional argument. Pack commands are
// documented in the more natural "object then flags" form, so move that single
// leading object behind its flags before parsing.
func leadingPositionalLast(args []string) []string {
	if len(args) < 2 || strings.HasPrefix(args[0], "-") {
		return args
	}
	out := append([]string(nil), args[1:]...)
	return append(out, args[0])
}

func loadPackArgument(path string) ([]*knowledge.Pack, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		pack, err := knowledge.LoadPack(path)
		if err != nil {
			return nil, err
		}
		return []*knowledge.Pack{pack}, nil
	}
	return knowledge.LoadPacksFromDirs([]string{path})
}
