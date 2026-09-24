package web

import (
	"io"
	"log"
	"sync"
	"time"
)

// LogRing keeps the most recent log lines in memory so the dashboard can show
// activity without mounting a log volume.
type LogRing struct {
	mu    sync.RWMutex
	lines []LogLine
	max   int
}

// LogLine is a single captured log entry.
type LogLine struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

func NewLogRing(max int) *LogRing {
	return &LogRing{max: max, lines: make([]LogLine, 0, max)}
}

// Snapshot returns a copy of the buffered lines, oldest first.
func (r *LogRing) Snapshot() []LogLine {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]LogLine, len(r.lines))
	copy(out, r.lines)
	return out
}

func (r *LogRing) add(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, LogLine{Time: time.Now(), Message: msg})
	if len(r.lines) > r.max {
		// Drop the oldest entries in one pass to keep appends cheap.
		r.lines = append(r.lines[:0], r.lines[len(r.lines)-r.max:]...)
	}
}

// TeeWriter mirrors log output into a ring buffer.
type TeeWriter struct {
	Dst io.Writer
	R   *LogRing
}

func (t TeeWriter) Write(p []byte) (int, error) {
	if t.R != nil {
		t.R.add(string(p))
	}
	if t.Dst != nil {
		return t.Dst.Write(p)
	}
	return len(p), nil
}

// NewLogger returns a logger that writes to the ring buffer and to stdout.
func NewLogger(out io.Writer, version string) (*log.Logger, *LogRing) {
	ring := NewLogRing(300)
	logger := log.New(TeeWriter{Dst: out, R: ring}, "",
		log.LstdFlags|log.Lmsgprefix)
	logger.SetPrefix("[" + version + "] ")
	return logger, ring
}
