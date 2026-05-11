package metrics

import (
	"fmt"
	"sync"
	"time"
)

// EventSeverity represents the severity level of an event.
type EventSeverity int

const (
	SeverityInfo EventSeverity = iota
	SeverityOK
	SeverityWarn
	SeverityError
)

// String returns the display label for the severity.
func (s EventSeverity) String() string {
	switch s {
	case SeverityError:
		return "ERROR"
	case SeverityWarn:
		return "WARN"
	case SeverityOK:
		return "OK"
	default:
		return "INFO"
	}
}

// Event represents a single cluster event.
type Event struct {
	Time     time.Time
	Severity EventSeverity
	Message  string
}

// EventRing is a thread-safe ring buffer holding the most recent N events.
type EventRing struct {
	mu     sync.Mutex
	events []Event
	cap    int
}

// NewEventRing creates a ring buffer with the given capacity.
func NewEventRing(capacity int) *EventRing {
	return &EventRing{
		events: make([]Event, 0, capacity),
		cap:    capacity,
	}
}

// Push adds an event, dropping the oldest if at capacity.
func (r *EventRing) Push(severity EventSeverity, format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := Event{
		Time:     time.Now(),
		Severity: severity,
		Message:  fmt.Sprintf(format, args...),
	}
	if len(r.events) >= r.cap {
		// Shift left by 1 to drop oldest
		copy(r.events, r.events[1:])
		r.events[len(r.events)-1] = e
	} else {
		r.events = append(r.events, e)
	}
}

// All returns a copy of all events, oldest first.
func (r *EventRing) All() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out
}

// Len returns the number of events in the buffer.
func (r *EventRing) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}
