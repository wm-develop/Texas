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
	// TypeTableRequestPreferencesSet 设置本人在本房间内是否接受换座/看牌申请。
	TypeTableRequestPreferencesSet MessageType = "table.request.preferences.set"
	// TypeTableRequestDeclined 只发给申请者：他的换座或看牌申请被拒绝了。
	TypeTableRequestDeclined       MessageType = "table.request.declined"
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
	TypeTableSpectateEnter         MessageType = "table.spectate.enter"
	TypeTableSpectateEntered       MessageType = "table.spectate.entered"
	TypeTableSeatTake              MessageType = "table.seat.take"
	TypeTableSeatTaken             MessageType = "table.seat.taken"
	TypeTableSpectatorSettingsSet  MessageType = "table.spectator.settings.set"
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
	// Scope 只在拒绝时有意义："once"（默认）只拒这一次；"requester" 不再接受
	// 这名申请者（换座在本房间内有效，看牌只在本手）；"everyone" 本手不再接受
	// 任何人的看牌申请（换座不支持）。
	Scope string `json:"scope,omitempty"`
}

// RequestPreferencesPayload 是 table.request.preferences.set 的载荷。两个字段都必填：
// 用指针是为了把「没传」和「传了 false」分开，漏传一个不能被当成把它关掉。
type RequestPreferencesPayload struct {
	AllowSeatSwapRequests     *bool `json:"allowSeatSwapRequests"`
	AllowHoleCardViewRequests *bool `json:"allowHoleCardViewRequests"`
}

// RequestDeclinedPayload 是 table.request.declined 的载荷，只发给申请者。
type RequestDeclinedPayload struct {
	// Kind 为 "seat_swap" 或 "hole_card_view"。
	Kind              string `json:"kind"`
	RequestID         string `json:"requestId"`
	TargetUserID      string `json:"targetUserId"`
	TargetDisplayName string `json:"targetDisplayName"`
	// Scope 与 RequestResponsePayload.Scope 同义，客户端据此选择提示文案。
	Scope string `json:"scope"`
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

// SpectatorSettingsSetPayload 是房主调整观战位设置的请求体，字段与快照里的
// spectatorSettings 一致。
type SpectatorSettingsSetPayload struct {
	FeeBigBlinds int  `json:"feeBigBlinds"`
	VoiceAllowed bool `json:"voiceAllowed"`
	ChatAllowed  bool `json:"chatAllowed"`
	EmoteAllowed bool `json:"emoteAllowed"`
}

// SpectateResultPayload 回复切换请求：pending 为真表示牌局进行中，意向已记录，
// 本手结束后生效。
type SpectateResultPayload struct {
	Pending bool `json:"pending"`
}
