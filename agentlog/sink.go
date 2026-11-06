package agentlog

import (
	"os"
	"path/filepath"
	"sync"
)

// Sink receives serialized JSONL lines.
type Sink interface {
	Write(line string) error
	Path() string
}

// defaultExportDir returns the default export directory: system temp dir + "/agent-log".
func defaultExportDir() string {
	return filepath.Join(os.TempDir(), "agent-log")
}

// FileSink appends JSONL lines to <exportDirectory>/agent-log.jsonl.
type FileSink struct {
	path string
	mu   sync.Mutex
}

// NewFileSink creates (or opens) the file sink for the given export directory.
// An empty exportDirectory falls back to the system default.
func NewFileSink(exportDir string) *FileSink {
	if exportDir == "" {
		exportDir = defaultExportDir()
	}
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		// Best-effort; Write will surface the real error if it cannot open.
		_ = err
	}
	return &FileSink{path: filepath.Join(exportDir, "agent-log.jsonl")}
}

// Write appends a single JSONL line to the file.
func (f *FileSink) Write(line string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	fd, err := os.OpenFile(f.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer fd.Close()
	if _, err := fd.WriteString(line + "\n"); err != nil {
		return err
	}
	return nil
}

// Path returns the absolute path of the underlying log file.
func (f *FileSink) Path() string {
	return f.path
}

// MemorySink keeps lines in an in-memory slice. Intended for tests.
type MemorySink struct {
	mu    sync.Mutex
	lines []string
}

// NewMemorySink creates an in-memory sink.
func NewMemorySink() *MemorySink {
	return &MemorySink{}
}

// Write appends a line to the in-memory slice.
func (m *MemorySink) Write(line string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lines = append(m.lines, line)
	return nil
}

// Path returns a placeholder path for the memory sink.
func (m *MemorySink) Path() string {
	return "<memory>"
}

// Lines returns a copy of the collected lines.
func (m *MemorySink) Lines() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.lines))
	copy(out, m.lines)
	return out
}

// LineCount returns the number of lines collected.
func (m *MemorySink) LineCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.lines)
}
