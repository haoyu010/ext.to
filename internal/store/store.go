// Package store persists which torrents have already been forwarded so a
// restart does not re-send the whole listing.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Record describes a torrent that has been seen, and optionally forwarded.
type Record struct {
	ID        int       `json:"id"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Category  string    `json:"category,omitempty"`
	Size      string    `json:"size,omitempty"`
	SizeMB    float64   `json:"size_mb,omitempty"`
	URL       string    `json:"url"`
	Magnet    string    `json:"magnet,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	Sent      bool      `json:"sent"`
	SentAt    time.Time `json:"sent_at,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// Stats summarises store contents for the web UI.
type Stats struct {
	Total     int        `json:"total"`
	Sent      int        `json:"sent"`
	Pending   int        `json:"pending"`
	Failed    int        `json:"failed"`
	LastRun   *time.Time `json:"last_run,omitempty"`
	LastError string     `json:"last_error,omitempty"`
}

type fileData struct {
	Records  map[string]Record `json:"records"`
	LastRun  time.Time         `json:"last_run"`
	LastErr  string            `json:"last_error,omitempty"`
	FirstRun bool              `json:"first_run_done"`
}

// Store is a concurrency-safe, JSON-backed record set.
type Store struct {
	mu       sync.RWMutex
	path     string
	records  map[string]Record
	lastRun  time.Time
	lastErr  string
	firstRun bool
	dirty    bool
}

// Open loads the store at path, creating it when missing.
func Open(path string) (*Store, error) {
	s := &Store{path: path, records: map[string]Record{}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var fd fileData
	if err := json.Unmarshal(b, &fd); err != nil {
		return nil, err
	}
	if fd.Records != nil {
		s.records = fd.Records
	}
	s.lastRun, s.lastErr, s.firstRun = fd.LastRun, fd.LastErr, fd.FirstRun
	return s, nil
}

// Has reports whether the torrent id is already known.
func (s *Store) Has(id int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.records[key(id)]
	return ok
}

// FirstRun reports whether the store has completed at least one scan. On the
// very first scan the forwarder only records a baseline instead of pushing
// the entire backlog to Telegram.
func (s *Store) FirstRun() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.firstRun
}

// MarkFirstRun records that the baseline scan has completed.
func (s *Store) MarkFirstRun() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.firstRun = true
	s.dirty = true
}

// Put records a torrent, preserving FirstSeen for ids already present.
func (s *Store) Put(r Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(r.ID)
	if old, ok := s.records[k]; ok {
		r.FirstSeen = old.FirstSeen
		if r.Magnet == "" {
			r.Magnet = old.Magnet
		}
	}
	if r.FirstSeen.IsZero() {
		r.FirstSeen = time.Now()
	}
	s.records[k] = r
	s.dirty = true
}

// MarkSent flags a record as forwarded.
func (s *Store) MarkSent(id int, magnet string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(id)
	r, ok := s.records[k]
	if !ok {
		r = Record{ID: id, FirstSeen: time.Now()}
	}
	r.Sent = true
	r.SentAt = time.Now()
	r.Error = ""
	if magnet != "" {
		r.Magnet = magnet
	}
	s.records[k] = r
	s.dirty = true
}

// MarkError records a delivery failure for a torrent.
func (s *Store) MarkError(id int, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(id)
	r, ok := s.records[k]
	if !ok {
		r = Record{ID: id, FirstSeen: time.Now()}
	}
	r.Error = msg
	s.records[k] = r
	s.dirty = true
}

// SetLastError stores the most recent run-level error.
func (s *Store) SetLastError(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErr = msg
	s.dirty = true
}

// TouchLastRun records the completion time of a scan.
func (s *Store) TouchLastRun() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRun = time.Now()
	s.dirty = true
}

// Stats returns aggregate counters.
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := Stats{Total: len(s.records), LastError: s.lastErr}
	if !s.lastRun.IsZero() {
		t := s.lastRun
		out.LastRun = &t
	}
	for _, r := range s.records {
		switch {
		case r.Sent:
			out.Sent++
		case r.Error != "":
			out.Failed++
		default:
			out.Pending++
		}
	}
	return out
}

// Recent returns the newest records, optionally filtered by delivery state.
func (s *Store) Recent(limit int, onlyStatus string) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, 0, len(s.records))
	for _, r := range s.records {
		switch onlyStatus {
		case "sent":
			if !r.Sent {
				continue
			}
		case "pending":
			if r.Sent || r.Error != "" {
				continue
			}
		case "failed":
			if r.Error == "" {
				continue
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FirstSeen.Equal(out[j].FirstSeen) {
			return out[i].ID > out[j].ID
		}
		return out[i].FirstSeen.After(out[j].FirstSeen)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Clear removes every record, including the first-run baseline.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = map[string]Record{}
	s.firstRun = false
	s.dirty = true
}

// Flush writes pending changes to disk. It is a no-op when nothing changed.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	fd := fileData{Records: s.records, LastRun: s.lastRun, LastErr: s.lastErr, FirstRun: s.firstRun}
	b, err := json.MarshalIndent(fd, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

func key(id int) string {
	return formatInt(id)
}

func formatInt(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
