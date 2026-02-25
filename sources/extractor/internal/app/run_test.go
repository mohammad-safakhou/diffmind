package app

import (
	"context"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
)

func TestRunRequiresOpenCode(t *testing.T) {
	cfg := config.Default()
	cfg.OpenCode.BaseURL = ""

	_, err := Run(context.Background(), RunInput{RepoPath: ".", Config: cfg})
	if err == nil {
		t.Fatalf("expected error when opencode url is missing")
	}
}
