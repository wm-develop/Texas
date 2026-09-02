package protocol

type MessageType string

const (
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
	TypeTableHoleCardsReveal       MessageType = "table.hole_cards.reveal"
	TypeTableHoleCardsRevealed     MessageType = "table.hole_cards.revealed"
	TypeTableHoleCardsRevealReject MessageType = "table.hole_cards.reveal.rejected"
	TypeTableHoleCardsViewRequest  MessageType = "table.hole_cards.view.request"
	TypeTableHoleCardsViewRespond  MessageType = "table.hole_cards.view.respond"
	TypeTableSeatChangeRequest     MessageType = "table.seat.change.request"
	TypeTableSeatSwapRespond       MessageType = "table.seat.swap.respond"
	TypeTableRunoutChoose          MessageType = "table.runout.choose"
	TypeTableTimeExtensionUse      MessageType = "table.time_extension.use"
	TypeTableTimeExtensionAccepted MessageType = "table.time_extension.accepted"
	TypeTableTimeExtensionRejected MessageType = "table.time_extension.rejected"
	TypeTableRebuy                 MessageType = "table.rebuy"
	TypeTableRebuyAccepted         MessageType = "table.rebuy.accepted"
	TypeTableRebuyRejected         MessageType = "table.rebuy.rejected"
	TypeTableVoiceStateSet         MessageType = "table.voice.state.set"
	TypeTableVoiceState            MessageType = "table.voice.state"
	TypeTableChatSend              MessageType = "table.chat.send"
	TypeTableChatAccepted          MessageType = "table.chat.accepted"
	TypeTableChatMessage           MessageType = "table.chat.message"
	TypeTableChatRejected          MessageType = "table.chat.rejected"
	TypeTablePlayerInteract        MessageType = "table.player.interact"
	TypeTablePlayerInteractAccept  MessageType = "table.player.interact.accepted"
	TypeTablePlayerInteraction     MessageType = "table.player.interaction"
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

type PlayerInteractPayload struct {
	TargetUserID string `json:"targetUserId"`
	Kind         string `json:"kind"`
}

type PlayerInteractionPayload struct {
	InteractionID     string `json:"interactionId"`
	FromUserID        string `json:"fromUserId"`
	FromDisplayName   string `json:"fromDisplayName"`
	TargetUserID      string `json:"targetUserId"`
	TargetDisplayName string `json:"targetDisplayName"`
	Kind              string `json:"kind"`
	SentAt            int64  `json:"sentAt"`
}

type VoiceStateSetPayload struct {
	Joined            bool `json:"joined"`
	MicrophoneEnabled bool `json:"microphoneEnabled"`
}

type RebuyPayload struct {
	Amount int64 `json:"amount"`
}

type HoleCardsViewRequestPayload struct {
	TargetUserID string `json:"targetUserId"`
}

type RequestResponsePayload struct {
	PendingRequestID string `json:"pendingRequestId"`
	Accept           bool   `json:"accept"`
}

type SeatChangeRequestPayload struct {
	TargetSeat int `json:"targetSeat"`
}

type RunoutChoosePayload struct {
	Count int `json:"count"`
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
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}
