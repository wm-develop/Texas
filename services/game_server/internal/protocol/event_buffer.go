package protocol

import (
	"errors"
	"sync"
)

type EventBuffer struct {
	mu       sync.RWMutex
	capacity int
	next     uint64
	events   []Envelope
}

func NewEventBuffer(capacity int) (*EventBuffer, error) {
	if capacity <= 0 {
		return nil, errors.New("event buffer capacity must be positive")
	}
	return &EventBuffer{capacity: capacity, next: 1}, nil
}

func (buffer *EventBuffer) Append(event Envelope) Envelope {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()

	event.Sequence = buffer.next
	buffer.next++
	event.Payload = append([]byte(nil), event.Payload...)
	buffer.events = append(buffer.events, event)
	if len(buffer.events) > buffer.capacity {
		copy(buffer.events, buffer.events[len(buffer.events)-buffer.capacity:])
		buffer.events = buffer.events[:buffer.capacity]
	}
	return cloneEnvelope(event)
}

// Since returns events strictly newer than lastSequence. The boolean is false
// when the requested gap is no longer retained and the caller must send a full
// table snapshot instead.
func (buffer *EventBuffer) Since(lastSequence uint64) ([]Envelope, bool) {
	buffer.mu.RLock()
	defer buffer.mu.RUnlock()

	latest := buffer.next - 1
	if lastSequence > latest {
		return nil, false
	}
	if len(buffer.events) == 0 || lastSequence == latest {
		return nil, true
	}
	oldest := buffer.events[0].Sequence
	if lastSequence+1 < oldest {
		return nil, false
	}

	result := make([]Envelope, 0, int(latest-lastSequence))
	for _, event := range buffer.events {
		if event.Sequence > lastSequence {
			result = append(result, cloneEnvelope(event))
		}
	}
	return result, true
}

func (buffer *EventBuffer) LatestSequence() uint64 {
	buffer.mu.RLock()
	defer buffer.mu.RUnlock()
	return buffer.next - 1
}

func cloneEnvelope(event Envelope) Envelope {
	event.Payload = append([]byte(nil), event.Payload...)
	return event
}
