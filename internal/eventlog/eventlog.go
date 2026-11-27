package eventlog

import (
	"sync"
	"time"
)

type EventLog struct {
	mu   sync.Mutex
	lines []string
	max  int
}

func New() *EventLog {
	return &EventLog{max: 200}
}

func (e *EventLog) Push(line string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ts := time.Now().Format("15:04:05")
	e.lines = append(e.lines, ts+"  "+line)
	if len(e.lines) > e.max {
		e.lines = e.lines[len(e.lines)-e.max:]
	}
}

func (e *EventLog) Snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.lines))
	copy(out, e.lines)
	return out
}
