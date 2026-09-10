package api

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LogBroker broadcasts log lines to all subscribed SSE clients.
type LogBroker struct {
	mu       sync.Mutex
	channels []chan string
	logFile  *os.File
}

// Write implements io.Writer. Prepends microsecond timestamp, writes to stderr,
// log file (if set), and broadcasts to SSE subscribers.
func (b *LogBroker) Write(p []byte) (int, error) {
	now := time.Now().Format("15:04:05.000000")
	line := now + " " + string(p)

	// Console (stderr) — write without holding the lock
	os.Stderr.WriteString(line)

	b.mu.Lock()
	// Log file
	if b.logFile != nil {
		b.logFile.WriteString(line)
	}
	// SSE subscribers
	for _, ch := range b.channels {
		select {
		case ch <- line:
		default:
		}
	}
	b.mu.Unlock()
	return len(p), nil
}

// OpenLogFile creates a timestamped log file under the given directory.
// Must be called before any logging occurs to ensure file capture.
func (b *LogBroker) OpenLogFile(logDir string) error {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("creating log dir %s: %w", logDir, err)
	}
	filename := filepath.Join(logDir, time.Now().Format("2006-01-02_150405")+".log")
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("creating log file %s: %w", filename, err)
	}
	b.mu.Lock()
	b.logFile = f
	b.mu.Unlock()
	fmt.Fprintf(os.Stderr, "[log] Writing logs to %s\n", filename)
	return nil
}

// CloseLogFile closes the log file if open.
func (b *LogBroker) CloseLogFile() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.logFile != nil {
		b.logFile.Close()
		b.logFile = nil
	}
}

// Subscribe returns a channel that receives log lines.
func (b *LogBroker) Subscribe() chan string {
	ch := make(chan string, 100)
	b.mu.Lock()
	b.channels = append(b.channels, ch)
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes the channel.
func (b *LogBroker) Unsubscribe(ch chan string) {
	b.mu.Lock()
	for i, c := range b.channels {
		if c == ch {
			b.channels = append(b.channels[:i], b.channels[i+1:]...)
			break
		}
	}
	b.mu.Unlock()
	close(ch)
}

// AttachToLog replaces the standard log output with this broker.
// Returns the original writer for restoration if needed.
func (b *LogBroker) AttachToLog() {
	log.SetOutput(b)
	log.SetFlags(0) // we prepend our own µs timestamps
	log.SetPrefix("")
}

// HandleLogs is the SSE handler for GET /api/logs.
func (b *LogBroker) HandleLogs(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	// Send initial connected message
	fmt.Fprintf(w, "data: [connected]\n\n")
	flusher.Flush()

	for {
		select {
		case line, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
