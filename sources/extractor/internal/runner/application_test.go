package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/app"
	"github.com/mohammad-safakhou/diffmind/internal/events"
)

type fakeApplication struct {
	runCalled bool
	err       error
}

func (f *fakeApplication) Run(context.Context, app.RunInput) (app.RunOutput, error) {
	f.runCalled = true
	return app.RunOutput{}, f.err
}

func TestRunnerUsesInjectedApplication(t *testing.T) {
	application := &fakeApplication{err: errors.New("stop")}
	runner := NewWithApplication(t.TempDir(), events.NewBus(10), application)
	id, err := runner.Start(context.Background(), StartParams{RepoPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	runner.WaitFor(id)
	if !application.runCalled {
		t.Fatal("injected application was not called")
	}
}
