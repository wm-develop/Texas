package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestActionSubmitJSONContract(t *testing.T) {
	payload, err := json.Marshal(ActionSubmitPayload{
		ActionID: "action_123",
		Action:   "raise",
		RaiseTo:  400,
	})
	if err != nil {
		t.Fatalf("Marshal payload: %v", err)
	}
	envelope := Envelope{
		Version:       1,
		Type:          string(TypeTableActionSubmit),
		RequestID:     "req_123",
		TableID:       "table_9527",
		HandID:        "hand_88",
		TableRevision: 52,
		Payload:       payload,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("Marshal envelope: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal envelope: %v", err)
	}
	if decoded["type"] != "table.action.submit" || decoded["tableRevision"] != float64(52) {
		t.Fatalf("unexpected envelope: %s", encoded)
	}
	decodedPayload := decoded["payload"].(map[string]any)
	if decodedPayload["actionId"] != "action_123" || decodedPayload["raiseTo"] != float64(400) {
		t.Fatalf("unexpected payload: %#v", decodedPayload)
	}
}

func TestEventBufferReplaysOrRequiresSnapshot(t *testing.T) {
	buffer, err := NewEventBuffer(3)
	if err != nil {
		t.Fatalf("NewEventBuffer: %v", err)
	}
	for revision := uint64(1); revision <= 5; revision++ {
		buffer.Append(Envelope{Version: 1, Type: "event", TableRevision: revision})
	}

	events, complete := buffer.Since(3)
	if !complete || len(events) != 2 || events[0].Sequence != 4 || events[1].Sequence != 5 {
		t.Fatalf("events=%#v complete=%v", events, complete)
	}
	if events, complete := buffer.Since(1); complete || events != nil {
		t.Fatalf("expired replay events=%#v complete=%v", events, complete)
	}
	if events, complete := buffer.Since(5); !complete || len(events) != 0 {
		t.Fatalf("current replay events=%#v complete=%v", events, complete)
	}
}

func TestEventBufferReturnsPayloadCopies(t *testing.T) {
	buffer, err := NewEventBuffer(2)
	if err != nil {
		t.Fatalf("NewEventBuffer: %v", err)
	}
	buffer.Append(Envelope{Version: 1, Type: "event", Payload: json.RawMessage(`{"ok":true}`)})
	first, _ := buffer.Since(0)
	first[0].Payload[0] = 'x'
	second, _ := buffer.Since(0)
	if reflect.DeepEqual(first[0].Payload, second[0].Payload) {
		t.Fatal("event payload aliases retained buffer")
	}
}
