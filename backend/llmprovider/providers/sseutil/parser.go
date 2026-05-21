// Package sseutil provides a simple Server-Sent Events (SSE) parser.
package sseutil

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

const DoneToken = "[DONE]"

// Event represents a single parsed SSE event.
type Event struct {
	ID   string
	Type string // value of "event:" line (default = "message")
	Data string // value of "data:" line(s); multi-line data joined with "\n"
}

// Scanner reads SSE events one at a time from an io.Reader.
// Usage mirrors bufio.Scanner: call Scan() in a loop, retrieve with Event().
type Scanner struct {
	br      *bufio.Reader
	current Event
	err     error
}

// NewScanner wraps an io.Reader for SSE scanning.
func NewScanner(r io.Reader) *Scanner {
	return &Scanner{br: bufio.NewReader(r)}
}

// Scan advances to the next SSE event.
// Returns false when the stream is exhausted or an error occurred.
func (s *Scanner) Scan() bool {
	var ev Event
	for {
		line, err := s.br.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")

		if line == "" {
			// Blank line = event boundary
			if ev.Data != "" || ev.Type != "" || ev.ID != "" {
				s.current = ev
				return true
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					s.err = err
				}
				return false
			}
			ev = Event{}
			continue
		}

		switch {
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimPrefix(data, " ")
			if ev.Data != "" {
				ev.Data += "\n"
			}
			ev.Data += data
		case strings.HasPrefix(line, "event:"):
			ev.Type = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "id:"):
			ev.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, ":"):
			// SSE comment line — skip
		}

		if err != nil {
			// End of stream mid-event: emit the partial event if it has data
			if ev.Data != "" {
				s.current = ev
				s.err = nil // EOF is not an error here
				return true
			}
			if !errors.Is(err, io.EOF) {
				s.err = err
			}
			return false
		}
	}
}

// Event returns the most recently scanned event.
func (s *Scanner) Event() Event { return s.current }

// Err returns the first non-EOF error encountered during scanning.
func (s *Scanner) Err() error { return s.err }

// IsDone reports whether the event data is the OpenAI "[DONE]" sentinel.
func (e Event) IsDone() bool { return e.Data == DoneToken }
