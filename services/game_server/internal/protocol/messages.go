package protocol

type MessageType string

const (
	TypeSystemHello                MessageType = "system.hello"
	TypeSystemPing                 MessageType = "system.ping"
	TypeSystemPong                 MessageType = "system.pong"
	TypeSystemError                MessageType = "system.error"
	TypeSessionAuthenticate        MessageType = "session.authenticate"
	TypeSessionAuthenticated       MessageType = "session.authenticated"
	TypeTableJoin                  MessageType = "table.join"
	TypeTableJoined                MessageType = "table.joined"
	TypeTableLeave                 MessageType = "table.leave"
	TypeTableReadySet              MessageType = "table.ready.set"
	TypeTableSnapshotRequest       MessageType = "table.snapshot.request"
	TypeTableSnapshot              MessageType = "table.snapshot"
	TypeTableReplayCompleted       MessageType = "table.replay.completed"
	TypeTableActionSubmit          MessageType = "table.action.submit"
	TypeTableActionRequired        MessageType = "table.action.required"
	TypeTableActionAccepted        MessageType = "table.action.accepted"
	TypeTableActionRejected        MessageType = "table.action.rejected"
	TypeTableHandStarted           MessageType = "table.hand.started"
	TypeTableHoleCardsDealt        MessageType = "table.hole_cards.dealt"
	TypeTableBoardDealt            MessageType = "table.board.dealt"
	TypeTableHandSettled           MessageType = "table.hand.settled"
	TypeTableTimeExtensionUse      MessageType = "table.time_extension.use"
	TypeTableTimeExtensionAccepted MessageType = "table.time_extension.accepted"
	TypeTableTimeExtensionRejected MessageType = "table.time_extension.rejected"
	TypeTableVoiceStateSet         MessageType = "table.voice.state.set"
	TypeTableVoiceState            MessageType = "table.voice.state"
	TypeTableChatSend              MessageType = "table.chat.send"
	TypeTableChatAccepted          MessageType = "table.chat.accepted"
	TypeTableChatMessage           MessageType = "table.chat.message"
	TypeTableChatRejected          MessageType = "table.chat.rejected"
)

type SessionAuthenticatePayload struct {
	AccessToken string `json:"accessToken"`
	DeviceID    string `json:"deviceId"`
}

type TableJoinPayload struct {
	LastSequence uint64 `json:"lastSequence,omitempty"`
}

type SnapshotRequestPayload struct {
	LastSequence uint64 `json:"lastSequence,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type ReplayCompletedPayload struct {
	LastSequence uint64 `json:"lastSequence"`
	Replayed     int    `json:"replayed"`
}

type ActionSubmitPayload struct {
	ActionID string `json:"actionId"`
	Action   string `json:"action"`
	RaiseTo  int64  `json:"raiseTo,omitempty"`
}

type ActionRequiredPayload struct {
	Seat       int      `json:"seat"`
	UserID     string   `json:"userId"`
	Deadline   int64    `json:"deadline"`
	ToCall     int64    `json:"toCall"`
	Actions    []string `json:"actions"`
	MinRaiseTo int64    `json:"minRaiseTo,omitempty"`
	MaxRaiseTo int64    `json:"maxRaiseTo,omitempty"`
}

type ChatSendPayload struct {
	ClientMessageID string `json:"clientMessageId"`
	Kind            string `json:"kind"`
	Content         string `json:"content"`
}

type VoiceStateSetPayload struct {
	Joined            bool `json:"joined"`
	MicrophoneEnabled bool `json:"microphoneEnabled"`
}

type VoiceMemberState struct {
	UserID            string `json:"userId"`
	DisplayName       string `json:"displayName"`
	Joined            bool   `json:"joined"`
	MicrophoneEnabled bool   `json:"microphoneEnabled"`
}

type VoiceStatePayload struct {
	Members []VoiceMemberState `json:"members"`
}

type ChatMessagePayload struct {
	MessageID       string `json:"messageId"`
	ClientMessageID string `json:"clientMessageId"`
	UserID          string `json:"userId"`
	DisplayName     string `json:"displayName"`
	Kind            string `json:"kind"`
	Content         string `json:"content"`
	SentAt          int64  `json:"sentAt"`
}

type ErrorPayload struct {
	Code            string `json:"code"`
	Message         string `json:"message,omitempty"`
	CurrentRevision uint64 `json:"currentRevision,omitempty"`
}
