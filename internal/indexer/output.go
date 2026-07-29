package indexer

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"sync"
)

const (
	maxChildOutputBytes = 1 << 20
	maxVerboseLineBytes = 64 << 10
)

// childOutput retains a bounded tail for failure classification while
// optionally forwarding complete child lines to the application logger.
// Stdout and stderr share one instance, so writes are serialized.
type childOutput struct {
	mu      sync.Mutex
	logger  *log.Logger
	prefix  string
	verbose bool

	captured []byte
	omitted  int64
	pending  []byte
}

func newChildOutput(logger *log.Logger, prefix string, verbose bool) *childOutput {
	return &childOutput{logger: logger, prefix: prefix, verbose: verbose}
}

func (w *childOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.capture(p)
	if w.verbose {
		w.pending = append(w.pending, p...)
		w.flushLines(false)
	}
	return len(p), nil
}

func (w *childOutput) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLines(true)
}

func (w *childOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()

	text := sanitizeChildText(w.captured, true)
	if w.omitted == 0 {
		return text
	}
	return fmt.Sprintf("[... %d earlier child-output bytes omitted ...]\n%s", w.omitted, text)
}

func (w *childOutput) capture(p []byte) {
	if len(p) >= maxChildOutputBytes {
		w.omitted += int64(len(w.captured) + len(p) - maxChildOutputBytes)
		w.captured = append(w.captured[:0], p[len(p)-maxChildOutputBytes:]...)
		return
	}
	overflow := len(w.captured) + len(p) - maxChildOutputBytes
	if overflow > 0 {
		copy(w.captured, w.captured[overflow:])
		w.captured = w.captured[:len(w.captured)-overflow]
		w.omitted += int64(overflow)
	}
	w.captured = append(w.captured, p...)
}

func (w *childOutput) flushLines(final bool) {
	for {
		if newline := bytes.IndexByte(w.pending, '\n'); newline >= 0 {
			w.logLine(w.pending[:newline])
			w.pending = w.pending[newline+1:]
			continue
		}
		if len(w.pending) > maxVerboseLineBytes {
			w.logLine(append(w.pending[:maxVerboseLineBytes:maxVerboseLineBytes], []byte(" [continued]")...))
			w.pending = w.pending[maxVerboseLineBytes:]
			continue
		}
		if final && len(w.pending) > 0 {
			w.logLine(w.pending)
			w.pending = nil
		}
		return
	}
}

func (w *childOutput) logLine(line []byte) {
	if w.logger == nil {
		return
	}
	line = bytes.TrimSuffix(line, []byte{'\r'})
	w.logger.Printf("%s%s", w.prefix, sanitizeChildText(line, false))
}

func sanitizeChildText(input []byte, allowNewline bool) string {
	text := strings.ToValidUTF8(string(input), "\uFFFD")
	var sanitized strings.Builder
	sanitized.Grow(len(text))
	for _, value := range text {
		if value == '\t' || allowNewline && value == '\n' {
			sanitized.WriteRune(value)
			continue
		}
		if value < 0x20 || value == 0x7f ||
			value >= 0x80 && value <= 0x9f {
			_, _ = fmt.Fprintf(&sanitized, "\\x%02x", value)
			continue
		}
		sanitized.WriteRune(value)
	}
	return sanitized.String()
}
