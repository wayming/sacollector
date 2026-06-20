package api

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// LogBroker broadcasts log lines to all subscribed SSE clients.
type LogBroker struct {
	mu       sync.Mutex
	channels []chan string
}

// Write implements io.Writer. Prepends microsecond timestamp, broadcasts to subscribers.
func (b *LogBroker) Write(p []byte) (int, error) {
	now := time.Now().Format("15:04:05.000000")
	line := now + " " + string(p)
	b.mu.Lock()
	for _, ch := range b.channels {
		select {
		case ch <- line:
		default:
		}
	}
	b.mu.Unlock()
	return len(p), nil
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
