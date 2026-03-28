package metrics

import "testing"

func TestEventRingPush(t *testing.T) {
	r := NewEventRing(3)

	r.Push(SeverityInfo, "first")
	r.Push(SeverityWarn, "second")
	r.Push(SeverityError, "third")

	events := r.All()
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Message != "first" {
		t.Errorf("events[0] = %q, want first", events[0].Message)
	}
	if events[2].Message != "third" {
		t.Errorf("events[2] = %q, want third", events[2].Message)
	}
}

func TestEventRingDropsOldest(t *testing.T) {
	r := NewEventRing(2)

	r.Push(SeverityInfo, "a")
	r.Push(SeverityInfo, "b")
	r.Push(SeverityInfo, "c")

	events := r.All()
	if len(events) != 2 {
		t.Fatalf("expected 2 events after overflow, got %d", len(events))
	}
	if events[0].Message != "b" {
		t.Errorf("oldest should be 'b' after drop, got %q", events[0].Message)
	}
	if events[1].Message != "c" {
		t.Errorf("newest should be 'c', got %q", events[1].Message)
	}
}

func TestEventRingEmpty(t *testing.T) {
	r := NewEventRing(5)

	events := r.All()
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
	if r.Len() != 0 {
		t.Fatalf("expected Len()=0, got %d", r.Len())
	}
}

func TestEventRingFormatArgs(t *testing.T) {
	r := NewEventRing(5)
	r.Push(SeverityWarn, "KV%% crossed %d on %s", 75, "worker-1")

	events := r.All()
	if events[0].Message != "KV% crossed 75 on worker-1" {
		t.Errorf("formatted message = %q", events[0].Message)
	}
}

func TestEventRingSeverityString(t *testing.T) {
	tests := []struct {
		sev  EventSeverity
		want string
	}{
		{SeverityInfo, "INFO"},
		{SeverityOK, "OK"},
		{SeverityWarn, "WARN"},
		{SeverityError, "ERROR"},
	}
	for _, tt := range tests {
		if got := tt.sev.String(); got != tt.want {
			t.Errorf("Severity(%d).String() = %q, want %q", tt.sev, got, tt.want)
		}
	}
}

func TestEventRingAllReturnsCopy(t *testing.T) {
	r := NewEventRing(3)
	r.Push(SeverityInfo, "x")

	events1 := r.All()
	events1[0].Message = "mutated"

	events2 := r.All()
	if events2[0].Message == "mutated" {
		t.Error("All() should return a copy, not a reference to internal state")
	}
}

func TestEventRingCapacityOne(t *testing.T) {
	r := NewEventRing(1)
	r.Push(SeverityInfo, "a")
	r.Push(SeverityInfo, "b")

	events := r.All()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Message != "b" {
		t.Errorf("expected 'b', got %q", events[0].Message)
	}
}
