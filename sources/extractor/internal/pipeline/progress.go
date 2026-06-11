package pipeline

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

type ProgressReporter struct {
	mu           sync.Mutex
	phase        string
	tip          string
	phaseTotal   int
	phaseDone    int
	startPercent int
	endPercent   int
	lastPercent  int
	tickerStop   chan struct{}
	sink         events.Sink
}

func NewProgressReporter() *ProgressReporter {
	p := &ProgressReporter{lastPercent: -1, tickerStop: make(chan struct{})}
	go p.tick()
	return p
}

// SetSink wires a live event sink so each progress tick is also published as
// a stage_progress event to the dashboard.
func (p *ProgressReporter) SetSink(s events.Sink) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sink = s
}

func (p *ProgressReporter) tick() {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-p.tickerStop:
			return
		case <-t.C:
			p.report(true)
		}
	}
}

func (p *ProgressReporter) Close() {
	close(p.tickerStop)
	p.report(true)
}

func (p *ProgressReporter) StartPhase(name string, total, startPercent, endPercent int, tip string) {
	if total < 0 {
		total = 0
	}
	p.mu.Lock()
	p.phase = name
	p.tip = tip
	p.phaseTotal = total
	p.phaseDone = 0
	p.startPercent = startPercent
	p.endPercent = endPercent
	p.mu.Unlock()
	p.report(true)
}

func (p *ProgressReporter) Advance() {
	p.mu.Lock()
	if p.phaseDone < p.phaseTotal {
		p.phaseDone++
	}
	p.mu.Unlock()
	p.report(false)
}

func (p *ProgressReporter) CompletePhase() {
	p.mu.Lock()
	p.phaseDone = p.phaseTotal
	p.mu.Unlock()
	p.report(true)
}

func (p *ProgressReporter) report(force bool) {
	p.mu.Lock()
	phase := p.phase
	tip := p.tip
	total := p.phaseTotal
	done := p.phaseDone
	start := p.startPercent
	end := p.endPercent
	last := p.lastPercent
	p.mu.Unlock()

	percent := start
	if end < start {
		end = start
	}
	if total <= 0 {
		percent = end
	} else {
		ratio := float64(done) / float64(total)
		if ratio < 0 {
			ratio = 0
		}
		if ratio > 1 {
			ratio = 1
		}
		percent = start + int(ratio*float64(end-start))
	}
	if percent > 100 {
		percent = 100
	}
	if !force && percent == last {
		return
	}

	bar := RenderProgressBar(percent, 20)
	if strings.TrimSpace(phase) == "" {
		phase = "starting"
	}
	if strings.TrimSpace(tip) == "" {
		tip = "Processing extraction pipeline"
	}

	util.Info("progress", "run status", map[string]any{
		"percent": percent,
		"bar":     bar,
		"phase":   phase,
		"done":    done,
		"total":   total,
		"tip":     tip,
	})

	p.mu.Lock()
	sink := p.sink
	p.lastPercent = percent
	p.mu.Unlock()

	if sink != nil {
		sink.Emit(events.Event{
			Kind:    events.KindStageProgress,
			Stage:   phase,
			Message: tip,
			Payload: map[string]any{
				"percent": percent,
				"done":    done,
				"total":   total,
			},
		})
	}
}

func RenderProgressBar(percent, width int) string {
	if width <= 0 {
		width = 10
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	filled := int(float64(width) * (float64(percent) / 100.0))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", width-filled) + "] " + fmt.Sprintf("%d%%", percent)
}
