package protocol

import "encoding/json"

type Envelope struct {
	Version       int             `json:"version"`
	Type          string          `json:"type"`
	RequestID     string          `json:"requestId,omitempty"`
	Sequence      uint64          `json:"sequence,omitempty"`
	TableID       string          `json:"tableId,omitempty"`
	HandID        string          `json:"handId,omitempty"`
	TableRevision uint64          `json:"tableRevision,omitempty"`
	ServerTime    int64           `json:"serverTime,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

func NewResponse(messageType string, requestID string, payload json.RawMessage) Envelope {
	return Envelope{
		Version:    1,
		Type:       messageType,
		RequestID:  requestID,
		ServerTime: unixMilliseconds(),
		Payload:    payload,
	}
}
