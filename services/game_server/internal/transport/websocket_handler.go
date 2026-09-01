package transport

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/chat"
	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/game/tablemanager"
	"texas/services/game_server/internal/protocol"
	"texas/services/game_server/internal/room"
)

type webSocketServer struct {
	logger         *slog.Logger
	accounts       *account.Service
	rooms          *room.Service
	tables         *tablemanager.Manager
	chat           *chat.Service
	hub            *tableHub
	requests       *protocol.RequestCache
	requestMu      sync.Mutex
	userLocks      map[string]*sync.Mutex
	originPatterns []string
	presence       *presenceTracker
	interactionMu  sync.Mutex
	interactionAt  map[string]time.Time
}

type webSocketClient struct {
	server     *webSocketServer
	connection *websocket.Conn
	writeMu    sync.Mutex
	user       account.User
	roomID     string
}

type tableHub struct {
	mu        sync.Mutex
	publishMu sync.Mutex
	logger    *slog.Logger
	clients   map[string]map[*webSocketClient]struct{}
	buffers   map[string]*protocol.EventBuffer
	voice     map[string]map[string]protocol.VoiceMemberState
}

func newWebSocketServer(
	logger *slog.Logger,
	options Options,
	presence *presenceTracker,
) *webSocketServer {
	server := &webSocketServer{
		logger: logger, accounts: options.Accounts, rooms: options.Rooms,
		tables: options.Tables, chat: options.Chat,
		hub: &tableHub{
			logger:  logger,
			clients: make(map[string]map[*webSocketClient]struct{}),
			buffers: make(map[string]*protocol.EventBuffer),
			voice:   make(map[string]map[string]protocol.VoiceMemberState),
		},
		requests:       protocol.NewRequestCache(256),
		userLocks:      make(map[string]*sync.Mutex),
		originPatterns: webSocketOriginPatterns(options.AllowedOrigins),
		presence:       presence,
		interactionAt:  make(map[string]time.Time),
	}
	if server.tables != nil {
		server.tables.SetSnapshotListener(func(roomID string) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.hub.broadcastSnapshots(ctx, server.tables, roomID, nil)
		})
	}
	return server
}

func (server *webSocketServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	server.serveHTTP(writer, request)
}

func (server *webSocketServer) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{
		OriginPatterns: server.originPatterns,
	})
	if err != nil {
		server.logger.Warn("websocket upgrade rejected", "error", err)
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(16 * 1024)
	client := &webSocketClient{server: server, connection: connection}
	defer client.disconnect()

	ctx := request.Context()
	for {
		var message protocol.Envelope
		if err := wsjson.Read(ctx, connection, &message); err != nil {
			if status := websocket.CloseStatus(err); status != websocket.StatusNormalClosure && status != websocket.StatusGoingAway {
				server.logger.Debug("websocket closed", "error", err)
			}
			return
		}
		if err := client.route(ctx, message); err != nil {
			server.logger.Debug("websocket response failed", "error", err)
			return
		}
	}
}

func webSocketOriginPatterns(allowedOrigins []string) []string {
	patterns := []string{"localhost:*", "127.0.0.1:*"}
	seen := map[string]struct{}{"localhost:*": {}, "127.0.0.1:*": {}}
	for _, origin := range allowedOrigins {
		origin = strings.TrimSuffix(strings.TrimSpace(origin), "/")
		if origin == "" {
			continue
		}
		if _, exists := seen[origin]; exists {
			continue
		}
		seen[origin] = struct{}{}
		patterns = append(patterns, origin)
	}
	return patterns
}

func (client *webSocketClient) route(ctx context.Context, message protocol.Envelope) error {
	if message.Version != 0 && message.Version != 1 {
		return client.sendError(message, protocol.TypeSystemError, "unsupported_protocol_version", 0)
	}
	switch protocol.MessageType(message.Type) {
	case protocol.TypeSystemPing:
		client.server.presence.touch(client.user.UserID)
		return client.write(protocol.NewResponse(string(protocol.TypeSystemPong), message.RequestID, nil))
	case protocol.TypeSessionAuthenticate:
		return client.authenticate(ctx, message)
	}
	if client.user.UserID == "" {
		return client.sendError(message, protocol.TypeSystemError, "authentication_required", 0)
	}
	if isIdempotentRequest(protocol.MessageType(message.Type)) {
		unlock := client.server.lockUserRequests(client.user.UserID)
		defer unlock()
		if message.RequestID == "" {
			return client.sendError(message, protocol.TypeSystemError, "request_id_required", 0)
		}
		if previous, ok := client.server.requests.Get(client.user.UserID, message.RequestID); ok {
			return client.write(previous)
		}
	}
	switch protocol.MessageType(message.Type) {
	case protocol.TypeTableJoin:
		return client.join(ctx, message)
	case protocol.TypeTableLeave:
		client.leave(ctx)
		return client.respond(message, protocol.TypeTableLeave, map[string]bool{"left": true})
	case protocol.TypeTableReadySet:
		return client.setReady(ctx, message)
	case protocol.TypeTableSnapshotRequest:
		return client.recoverEvents(ctx, message)
	case protocol.TypeTableActionSubmit:
		return client.submitAction(ctx, message)
	case protocol.TypeTableHoleCardsReveal:
		return client.showHoleCards(ctx, message)
	case protocol.TypeTableHoleCardsViewRequest:
		return client.requestHoleCardsView(ctx, message)
	case protocol.TypeTableHoleCardsViewRespond:
		return client.respondHoleCardsView(ctx, message)
	case protocol.TypeTableSeatChangeRequest:
		return client.requestSeatChange(ctx, message)
	case protocol.TypeTableSeatSwapRespond:
		return client.respondSeatSwap(ctx, message)
	case protocol.TypeTableRunoutChoose:
		return client.chooseRunout(ctx, message)
	case protocol.TypeTableTimeExtensionUse:
		return client.useTimeExtension(ctx, message)
	case protocol.TypeTableRebuy:
		return client.rebuy(ctx, message)
	case protocol.TypeTableVoiceStateSet:
		return client.setVoiceState(message)
	case protocol.TypeTableChatSend:
		return client.sendChat(ctx, message)
	case protocol.TypeTablePlayerInteract:
		return client.sendPlayerInteraction(ctx, message)
	default:
		return client.sendError(message, protocol.TypeSystemError, "unsupported_message_type", 0)
	}
}

func (client *webSocketClient) rebuy(ctx context.Context, message protocol.Envelope) error {
	if client.roomID == "" || client.server.tables == nil {
		return client.sendError(message, protocol.TypeTableRebuyRejected, "table_not_joined", 0)
	}
	var payload protocol.RebuyPayload
	if !decodePayload(message.Payload, &payload) {
		return client.sendError(message, protocol.TypeTableRebuyRejected, "invalid_request", 0)
	}
	snapshot, err := client.server.tables.Rebuy(ctx, client.user.UserID, client.roomID, message.RequestID, payload.Amount)
	if err != nil {
		return client.sendError(message, protocol.TypeTableRebuyRejected, errorCode(err), 0)
	}
	if err := client.respond(message, protocol.TypeTableRebuyAccepted, map[string]int64{"amount": payload.Amount}); err != nil {
		return err
	}
	return client.server.hub.broadcastSnapshots(ctx, client.server.tables, client.roomID, &snapshot)
}

func (client *webSocketClient) useTimeExtension(ctx context.Context, message protocol.Envelope) error {
	if client.roomID == "" || client.server.tables == nil {
		return client.sendError(message, protocol.TypeTableTimeExtensionRejected, "table_not_joined", 0)
	}
	snapshot, err := client.server.tables.UseTimeExtension(ctx, client.user.UserID, client.roomID)
	if err != nil {
		return client.sendError(message, protocol.TypeTableTimeExtensionRejected, errorCode(err), 0)
	}
	remaining := 0
	for _, seat := range snapshot.Seats {
		if seat.UserID == client.user.UserID {
			remaining = seat.TimeExtensions
			break
		}
	}
	if err := client.respond(message, protocol.TypeTableTimeExtensionAccepted, map[string]int{
		"remaining": remaining,
	}); err != nil {
		return err
	}
	return client.server.hub.broadcastSnapshots(ctx, client.server.tables, client.roomID, &snapshot)
}

func (client *webSocketClient) authenticate(ctx context.Context, message protocol.Envelope) error {
	if client.server.accounts == nil {
		return client.sendError(message, protocol.TypeSystemError, "service_unavailable", 0)
	}
	var payload protocol.SessionAuthenticatePayload
	if !decodePayload(message.Payload, &payload) {
		return client.sendError(message, protocol.TypeSystemError, "invalid_request", 0)
	}
	user, err := client.server.accounts.Authenticate(ctx, payload.AccessToken)
	if err != nil {
		return client.sendError(message, protocol.TypeSystemError, errorCode(err), 0)
	}
	if client.user.UserID != "" && client.user.UserID != user.UserID {
		client.leave(ctx)
	}
	client.user = user
	client.server.presence.touch(user.UserID)
	return client.write(response(message, protocol.TypeSessionAuthenticated, map[string]any{
		"user": user, "deviceId": payload.DeviceID,
	}))
}

func (server *webSocketServer) disconnectUsers(roomID string, userIDs []string) {
	targets := make(map[string]struct{}, len(userIDs))
	for _, userID := range userIDs {
		targets[userID] = struct{}{}
	}
	for _, client := range server.hub.clientsFor(roomID) {
		if _, exists := targets[client.user.UserID]; !exists {
			continue
		}
		_ = client.connection.Close(websocket.StatusPolicyViolation, "removed_by_administrator")
	}
}

func (client *webSocketClient) join(ctx context.Context, message protocol.Envelope) error {
	if client.server.rooms == nil || client.server.tables == nil {
		return client.sendError(message, protocol.TypeSystemError, "service_unavailable", 0)
	}
	var joinPayload protocol.TableJoinPayload
	if len(message.Payload) > 0 && !decodePayload(message.Payload, &joinPayload) {
		return client.sendError(message, protocol.TypeSystemError, "invalid_request", 0)
	}
	roomID := message.TableID
	if roomID == "" {
		current, err := client.server.rooms.Current(ctx, client.user.UserID)
		if err != nil {
			return client.sendError(message, protocol.TypeSystemError, errorCode(err), 0)
		}
		roomID = current.RoomID
	}
	snapshot, err := client.server.tables.Join(ctx, client.user.UserID, roomID)
	if err != nil {
		return client.sendError(message, protocol.TypeSystemError, errorCode(err), 0)
	}
	if client.roomID != "" && client.roomID != roomID {
		client.leave(ctx)
	}
	client.roomID = roomID
	client.server.hub.register(client)
	history := []chat.Message(nil)
	if client.server.chat != nil {
		history = client.server.chat.History(roomID, 50)
	}
	if err := client.write(response(message, protocol.TypeTableJoined, map[string]any{
		"roomId": roomID, "chatHistory": history,
		"resumedFromSequence": joinPayload.LastSequence,
		"voiceMembers":        client.server.hub.voiceStatesFor(roomID),
	})); err != nil {
		return err
	}
	return client.server.hub.broadcastSnapshots(ctx, client.server.tables, roomID, &snapshot)
}

func (client *webSocketClient) setVoiceState(message protocol.Envelope) error {
	if client.roomID == "" {
		return client.sendError(message, protocol.TypeSystemError, "table_not_joined", 0)
	}
	var payload protocol.VoiceStateSetPayload
	if !decodePayload(message.Payload, &payload) {
		return client.sendError(message, protocol.TypeSystemError, "invalid_request", 0)
	}
	client.server.hub.setVoiceState(client.roomID, protocol.VoiceMemberState{
		UserID: client.user.UserID, DisplayName: client.user.DisplayName,
		Joined: payload.Joined, MicrophoneEnabled: payload.Joined && payload.MicrophoneEnabled,
	})
	if err := client.respond(message, protocol.TypeTableVoiceStateSet, payload); err != nil {
		return err
	}
	return client.server.hub.broadcastVoiceStates(client.roomID)
}

func (client *webSocketClient) setReady(ctx context.Context, message protocol.Envelope) error {
	if client.roomID == "" {
		return client.sendError(message, protocol.TypeSystemError, "table_not_joined", 0)
	}
	var payload struct {
		Ready bool `json:"ready"`
	}
	if !decodePayload(message.Payload, &payload) {
		return client.sendError(message, protocol.TypeSystemError, "invalid_request", 0)
	}
	snapshot, err := client.server.tables.SetReady(ctx, client.user.UserID, payload.Ready)
	if err != nil {
		return client.sendError(message, protocol.TypeSystemError, errorCode(err), 0)
	}
	if err := client.respond(message, protocol.TypeTableReadySet, map[string]bool{"ready": payload.Ready}); err != nil {
		return err
	}
	return client.server.hub.broadcastSnapshots(ctx, client.server.tables, client.roomID, &snapshot)
}

func (client *webSocketClient) submitAction(ctx context.Context, message protocol.Envelope) error {
	if client.roomID == "" {
		return client.sendError(message, protocol.TypeTableActionRejected, "table_not_joined", 0)
	}
	var payload protocol.ActionSubmitPayload
	if !decodePayload(message.Payload, &payload) {
		return client.sendError(message, protocol.TypeTableActionRejected, "invalid_request", 0)
	}
	result, snapshot, err := client.server.tables.SubmitAction(ctx, client.user.UserID, client.roomID, holdem.ActionRequest{
		ActionID: payload.ActionID, HandID: message.HandID, TableRevision: message.TableRevision,
		Action: holdem.ActionType(payload.Action), RaiseTo: payload.RaiseTo,
	})
	if err != nil {
		code := errorCode(err)
		if code == "internal_error" {
			client.server.logger.Error(
				"table action failed",
				"error", err,
				"room_id", client.roomID,
				"user_id", client.user.UserID,
				"hand_id", message.HandID,
			)
		}
		return client.sendError(message, protocol.TypeTableActionRejected, code, revisionFromError(err))
	}
	if err := client.respond(message, protocol.TypeTableActionAccepted, result); err != nil {
		return err
	}
	return client.server.hub.broadcastSnapshots(ctx, client.server.tables, client.roomID, &snapshot)
}

func (client *webSocketClient) showHoleCards(ctx context.Context, message protocol.Envelope) error {
	if client.roomID == "" {
		return client.sendError(message, protocol.TypeTableHoleCardsRevealReject, "table_not_joined", 0)
	}
	snapshot, err := client.server.tables.ShowHoleCards(ctx, client.user.UserID, client.roomID)
	if err != nil {
		return client.sendError(message, protocol.TypeTableHoleCardsRevealReject, errorCode(err), 0)
	}
	if err := client.respond(message, protocol.TypeTableHoleCardsRevealed, map[string]bool{"revealed": true}); err != nil {
		return err
	}
	return client.server.hub.broadcastSnapshots(ctx, client.server.tables, client.roomID, &snapshot)
}

func (client *webSocketClient) requestHoleCardsView(ctx context.Context, message protocol.Envelope) error {
	if client.roomID == "" {
		return client.sendError(message, protocol.TypeSystemError, "table_not_joined", 0)
	}
	var payload protocol.HoleCardsViewRequestPayload
	if !decodePayload(message.Payload, &payload) {
		return client.sendError(message, protocol.TypeSystemError, "invalid_request", 0)
	}
	snapshot, err := client.server.tables.RequestHoleCardView(
		ctx, client.user.UserID, client.roomID, payload.TargetUserID, message.RequestID,
	)
	if err != nil {
		return client.sendError(message, protocol.TypeSystemError, errorCode(err), 0)
	}
	if err := client.respond(message, protocol.TypeTableHoleCardsViewRequest, map[string]bool{"requested": true}); err != nil {
		return err
	}
	return client.server.hub.broadcastSnapshots(ctx, client.server.tables, client.roomID, &snapshot)
}

func (client *webSocketClient) respondHoleCardsView(ctx context.Context, message protocol.Envelope) error {
	if client.roomID == "" {
		return client.sendError(message, protocol.TypeSystemError, "table_not_joined", 0)
	}
	var payload protocol.RequestResponsePayload
	if !decodePayload(message.Payload, &payload) {
		return client.sendError(message, protocol.TypeSystemError, "invalid_request", 0)
	}
	snapshot, err := client.server.tables.RespondHoleCardView(
		ctx, client.user.UserID, client.roomID, payload.PendingRequestID, payload.Accept,
	)
	if err != nil {
		return client.sendError(message, protocol.TypeSystemError, errorCode(err), 0)
	}
	if err := client.respond(message, protocol.TypeTableHoleCardsViewRespond, map[string]bool{"accepted": payload.Accept}); err != nil {
		return err
	}
	return client.server.hub.broadcastSnapshots(ctx, client.server.tables, client.roomID, &snapshot)
}

func (client *webSocketClient) requestSeatChange(ctx context.Context, message protocol.Envelope) error {
	if client.roomID == "" {
		return client.sendError(message, protocol.TypeSystemError, "table_not_joined", 0)
	}
	var payload protocol.SeatChangeRequestPayload
	if !decodePayload(message.Payload, &payload) {
		return client.sendError(message, protocol.TypeSystemError, "invalid_request", 0)
	}
	snapshot, err := client.server.tables.RequestSeatChange(
		ctx, client.user.UserID, client.roomID, payload.TargetSeat, message.RequestID,
	)
	if err != nil {
		return client.sendError(message, protocol.TypeSystemError, errorCode(err), 0)
	}
	if err := client.respond(message, protocol.TypeTableSeatChangeRequest, map[string]bool{"requested": true}); err != nil {
		return err
	}
	return client.server.hub.broadcastSnapshots(ctx, client.server.tables, client.roomID, &snapshot)
}

func (client *webSocketClient) respondSeatSwap(ctx context.Context, message protocol.Envelope) error {
	if client.roomID == "" {
		return client.sendError(message, protocol.TypeSystemError, "table_not_joined", 0)
	}
	var payload protocol.RequestResponsePayload
	if !decodePayload(message.Payload, &payload) {
		return client.sendError(message, protocol.TypeSystemError, "invalid_request", 0)
	}
	snapshot, err := client.server.tables.RespondSeatSwap(
		ctx, client.user.UserID, client.roomID, payload.PendingRequestID, payload.Accept,
	)
	if err != nil {
		return client.sendError(message, protocol.TypeSystemError, errorCode(err), 0)
	}
	if err := client.respond(message, protocol.TypeTableSeatSwapRespond, map[string]bool{"accepted": payload.Accept}); err != nil {
		return err
	}
	return client.server.hub.broadcastSnapshots(ctx, client.server.tables, client.roomID, &snapshot)
}

func (client *webSocketClient) chooseRunout(ctx context.Context, message protocol.Envelope) error {
	if client.roomID == "" {
		return client.sendError(message, protocol.TypeSystemError, "table_not_joined", 0)
	}
	var payload protocol.RunoutChoosePayload
	if !decodePayload(message.Payload, &payload) {
		return client.sendError(message, protocol.TypeSystemError, "invalid_request", 0)
	}
	snapshot, err := client.server.tables.SubmitRunoutChoice(
		ctx, client.user.UserID, client.roomID, payload.Count,
	)
	if err != nil {
		return client.sendError(message, protocol.TypeSystemError, errorCode(err), 0)
	}
	if err := client.respond(message, protocol.TypeTableRunoutChoose, map[string]int{"count": payload.Count}); err != nil {
		return err
	}
	return client.server.hub.broadcastSnapshots(ctx, client.server.tables, client.roomID, &snapshot)
}

func (client *webSocketClient) sendChat(ctx context.Context, message protocol.Envelope) error {
	if client.roomID == "" || client.server.chat == nil || client.server.rooms == nil {
		return client.sendError(message, protocol.TypeTableChatRejected, "table_not_joined", 0)
	}
	var payload protocol.ChatSendPayload
	if !decodePayload(message.Payload, &payload) {
		return client.sendError(message, protocol.TypeTableChatRejected, "invalid_request", 0)
	}
	if _, err := client.server.rooms.GetForMember(ctx, client.user.UserID, client.roomID); err != nil {
		return client.sendError(message, protocol.TypeTableChatRejected, errorCode(err), 0)
	}
	accepted, err := client.server.chat.Send(chat.Sender{
		UserID: client.user.UserID, DisplayName: client.user.DisplayName,
		TableID: client.roomID, CanChat: true,
	}, chat.Request{
		ClientMessageID: payload.ClientMessageID, Kind: chat.Kind(payload.Kind), Content: payload.Content,
	})
	if err != nil {
		return client.sendError(message, protocol.TypeTableChatRejected, errorCode(err), 0)
	}
	if err := client.respond(message, protocol.TypeTableChatAccepted, accepted); err != nil {
		return err
	}
	return client.server.hub.broadcast(client.roomID, protocol.TypeTableChatMessage, chatPayload(accepted))
}

func (client *webSocketClient) sendPlayerInteraction(ctx context.Context, message protocol.Envelope) error {
	if client.roomID == "" || client.server.rooms == nil {
		return client.sendError(message, protocol.TypeSystemError, "table_not_joined", 0)
	}
	var payload protocol.PlayerInteractPayload
	if !decodePayload(message.Payload, &payload) ||
		(payload.Kind != "praise" && payload.Kind != "taunt") ||
		payload.TargetUserID == "" || payload.TargetUserID == client.user.UserID {
		return client.sendError(message, protocol.TypeSystemError, "invalid_player_interaction", 0)
	}
	roomValue, err := client.server.rooms.GetForMember(ctx, client.user.UserID, client.roomID)
	if err != nil {
		return client.sendError(message, protocol.TypeSystemError, errorCode(err), 0)
	}
	var targetName string
	for _, member := range roomValue.Members {
		if member.UserID == payload.TargetUserID {
			targetName = member.DisplayName
			break
		}
	}
	if targetName == "" {
		return client.sendError(message, protocol.TypeSystemError, "player_not_at_table", 0)
	}
	now := time.Now()
	client.server.interactionMu.Lock()
	lastSent := client.server.interactionAt[client.user.UserID]
	if now.Sub(lastSent) < 1500*time.Millisecond {
		client.server.interactionMu.Unlock()
		return client.sendError(message, protocol.TypeSystemError, "player_interaction_too_frequent", 0)
	}
	client.server.interactionAt[client.user.UserID] = now
	client.server.interactionMu.Unlock()

	result := protocol.PlayerInteractionPayload{
		InteractionID: message.RequestID, FromUserID: client.user.UserID,
		FromDisplayName: client.user.DisplayName, TargetUserID: payload.TargetUserID,
		TargetDisplayName: targetName, Kind: payload.Kind, SentAt: now.UnixMilli(),
	}
	if err := client.respond(message, protocol.TypeTablePlayerInteractAccept, result); err != nil {
		return err
	}
	return client.server.hub.broadcast(client.roomID, protocol.TypeTablePlayerInteraction, result)
}

func (client *webSocketClient) recoverEvents(ctx context.Context, message protocol.Envelope) error {
	if client.roomID == "" || client.server.tables == nil {
		return client.sendError(message, protocol.TypeSystemError, "table_not_joined", 0)
	}
	var payload protocol.SnapshotRequestPayload
	if len(message.Payload) > 0 && !decodePayload(message.Payload, &payload) {
		return client.sendError(message, protocol.TypeSystemError, "invalid_request", 0)
	}
	if payload.LastSequence > 0 {
		replayed, complete := client.server.hub.replay(client, payload.LastSequence)
		if complete {
			return client.write(response(message, protocol.TypeTableReplayCompleted, protocol.ReplayCompletedPayload{
				LastSequence: client.server.hub.latestSequence(client.roomID),
				Replayed:     replayed,
			}))
		}
	}
	snapshot, err := client.server.tables.Snapshot(ctx, client.user.UserID, client.roomID)
	if err != nil {
		return client.sendError(message, protocol.TypeSystemError, errorCode(err), 0)
	}
	return client.write(snapshotEnvelope(snapshot, client.server.hub.latestSequence(client.roomID), message.RequestID))
}

func (client *webSocketClient) disconnect() {
	roomID := client.roomID
	if roomID == "" {
		return
	}
	client.server.hub.unregister(client)
	client.server.hub.removeVoiceState(roomID, client.user.UserID)
	client.server.tables.Disconnect(context.Background(), client.user.UserID, roomID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = client.server.hub.broadcastSnapshots(ctx, client.server.tables, roomID, nil)
	_ = client.server.hub.broadcastVoiceStates(roomID)
	client.roomID = ""
}

func (client *webSocketClient) leave(ctx context.Context) {
	roomID := client.roomID
	if roomID == "" {
		return
	}
	client.server.hub.unregister(client)
	client.server.hub.removeVoiceState(roomID, client.user.UserID)
	client.server.tables.Disconnect(ctx, client.user.UserID, roomID)
	client.roomID = ""
	_ = client.server.hub.broadcastSnapshots(ctx, client.server.tables, roomID, nil)
	_ = client.server.hub.broadcastVoiceStates(roomID)
}

func (client *webSocketClient) write(message protocol.Envelope) error {
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return wsjson.Write(ctx, client.connection, message)
}

func (client *webSocketClient) respond(
	request protocol.Envelope,
	messageType protocol.MessageType,
	payload any,
) error {
	message := response(request, messageType, payload)
	if isIdempotentRequest(protocol.MessageType(request.Type)) {
		client.server.requests.Put(client.user.UserID, request.RequestID, message)
	}
	return client.write(message)
}

func (client *webSocketClient) sendError(
	request protocol.Envelope,
	messageType protocol.MessageType,
	code string,
	currentRevision uint64,
) error {
	message := response(request, messageType, protocol.ErrorPayload{
		Code: code, CurrentRevision: currentRevision,
	})
	if isIdempotentRequest(protocol.MessageType(request.Type)) {
		client.server.requests.Put(client.user.UserID, request.RequestID, message)
	}
	return client.write(message)
}

func (hub *tableHub) register(client *webSocketClient) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.clients[client.roomID] == nil {
		hub.clients[client.roomID] = make(map[*webSocketClient]struct{})
	}
	hub.clients[client.roomID][client] = struct{}{}
}

func (hub *tableHub) unregister(client *webSocketClient) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	clients := hub.clients[client.roomID]
	delete(clients, client)
	if len(clients) == 0 {
		delete(hub.clients, client.roomID)
	}
}

func (hub *tableHub) bufferFor(roomID string) *protocol.EventBuffer {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	buffer := hub.buffers[roomID]
	if buffer == nil {
		buffer, _ = protocol.NewEventBuffer(256)
		hub.buffers[roomID] = buffer
	}
	return buffer
}

func (hub *tableHub) latestSequence(roomID string) uint64 {
	return hub.bufferFor(roomID).LatestSequence()
}

func (hub *tableHub) clientsFor(roomID string) []*webSocketClient {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	result := make([]*webSocketClient, 0, len(hub.clients[roomID]))
	for client := range hub.clients[roomID] {
		result = append(result, client)
	}
	return result
}

func (hub *tableHub) setVoiceState(roomID string, state protocol.VoiceMemberState) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.voice[roomID] == nil {
		hub.voice[roomID] = make(map[string]protocol.VoiceMemberState)
	}
	if !state.Joined {
		delete(hub.voice[roomID], state.UserID)
		return
	}
	hub.voice[roomID][state.UserID] = state
}

func (hub *tableHub) removeVoiceState(roomID, userID string) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	delete(hub.voice[roomID], userID)
	if len(hub.voice[roomID]) == 0 {
		delete(hub.voice, roomID)
	}
}

func (hub *tableHub) voiceStatesFor(roomID string) []protocol.VoiceMemberState {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	result := make([]protocol.VoiceMemberState, 0, len(hub.voice[roomID]))
	for _, state := range hub.voice[roomID] {
		result = append(result, state)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].UserID < result[right].UserID
	})
	return result
}

func (hub *tableHub) broadcastVoiceStates(roomID string) error {
	return hub.broadcast(roomID, protocol.TypeTableVoiceState, protocol.VoiceStatePayload{
		Members: hub.voiceStatesFor(roomID),
	})
}

func (hub *tableHub) broadcastSnapshots(
	ctx context.Context,
	tables *tablemanager.Manager,
	roomID string,
	known *tablemanager.Snapshot,
) error {
	hub.publishMu.Lock()
	defer hub.publishMu.Unlock()
	marker := hub.bufferFor(roomID).Append(protocol.Envelope{
		Version: 1,
		Type:    string(protocol.TypeTableSnapshot),
		TableID: roomID,
	})
	sequence := marker.Sequence
	clients := hub.clientsFor(roomID)
	delivered := 0
	for _, client := range clients {
		var snapshot tablemanager.Snapshot
		var err error
		if known != nil && len(clients) == 1 && known.RoomID == roomID {
			snapshot = *known
		} else {
			snapshot, err = tables.Snapshot(ctx, client.user.UserID, roomID)
			if err != nil {
				// A snapshot that cannot be built leaves this client frozen on
				// stale state. Never fail silently: a fault that hits every
				// client stalls the whole table with no other visible symptom.
				hub.logError(
					"table snapshot generation failed", err,
					"room_id", roomID, "user_id", client.user.UserID, "sequence", sequence,
				)
				continue
			}
		}
		if err := client.write(snapshotEnvelope(snapshot, sequence, "")); err != nil {
			hub.logError(
				"table snapshot delivery failed", err,
				"room_id", roomID, "user_id", client.user.UserID, "sequence", sequence,
			)
			continue
		}
		delivered++
	}
	if len(clients) > 0 && delivered == 0 {
		// Every client missed this revision: the table is effectively frozen
		// for everyone and needs operator attention, not just a per-client note.
		hub.logError(
			"table snapshot broadcast reached no client", nil,
			"room_id", roomID, "sequence", sequence, "clients", len(clients),
		)
	}
	return nil
}

func (hub *tableHub) logError(message string, err error, attributes ...any) {
	if hub.logger == nil {
		return
	}
	if err != nil {
		attributes = append(attributes, "error", err)
	}
	hub.logger.Error(message, attributes...)
}

func (hub *tableHub) broadcast(roomID string, messageType protocol.MessageType, payload any) error {
	hub.publishMu.Lock()
	defer hub.publishMu.Unlock()
	message := response(protocol.Envelope{}, messageType, payload)
	message.TableID = roomID
	message = hub.bufferFor(roomID).Append(message)
	for _, client := range hub.clientsFor(roomID) {
		if err := client.write(message); err != nil {
			hub.logError(
				"table event delivery failed", err,
				"room_id", roomID, "user_id", client.user.UserID,
				"type", string(messageType), "sequence", message.Sequence,
			)
			continue
		}
	}
	return nil
}

func (hub *tableHub) replay(client *webSocketClient, lastSequence uint64) (int, bool) {
	hub.publishMu.Lock()
	defer hub.publishMu.Unlock()
	events, complete := hub.bufferFor(client.roomID).Since(lastSequence)
	if !complete {
		return 0, false
	}
	for _, event := range events {
		// Snapshot payloads are personalized and therefore never retained in the
		// shared room buffer. A snapshot marker means replay is unsafe.
		if event.Type == string(protocol.TypeTableSnapshot) {
			return 0, false
		}
	}
	for _, event := range events {
		if err := client.write(event); err != nil {
			return 0, false
		}
	}
	return len(events), true
}

func snapshotEnvelope(snapshot tablemanager.Snapshot, sequence uint64, requestID string) protocol.Envelope {
	message := response(protocol.Envelope{RequestID: requestID}, protocol.TypeTableSnapshot, snapshot)
	message.Sequence = sequence
	message.TableID = snapshot.RoomID
	message.HandID = snapshot.HandID
	message.TableRevision = snapshot.TableRevision
	return message
}

func response(request protocol.Envelope, messageType protocol.MessageType, payload any) protocol.Envelope {
	encoded, _ := json.Marshal(payload)
	message := protocol.NewResponse(string(messageType), request.RequestID, encoded)
	message.TableID = request.TableID
	message.HandID = request.HandID
	message.TableRevision = request.TableRevision
	return message
}

func chatPayload(message chat.Message) protocol.ChatMessagePayload {
	return protocol.ChatMessagePayload{
		MessageID: message.MessageID, ClientMessageID: message.ClientMessageID,
		UserID: message.UserID, DisplayName: message.DisplayName,
		Kind: string(message.Kind), Content: message.Content, SentAt: message.SentAt.UnixMilli(),
	}
}

func decodePayload(raw json.RawMessage, target any) bool {
	return len(raw) > 0 && json.Unmarshal(raw, target) == nil
}

func errorCode(err error) string {
	var accountError account.Error
	if errors.As(err, &accountError) {
		return accountError.Code
	}
	var roomError room.Error
	if errors.As(err, &roomError) {
		return roomError.Code
	}
	var bankrollError bankroll.Error
	if errors.As(err, &bankrollError) {
		return bankrollError.Code
	}
	var ruleError holdem.RuleError
	if errors.As(err, &ruleError) {
		return ruleError.Code
	}
	var chatError chat.Error
	if errors.As(err, &chatError) {
		return chatError.Code
	}
	return "internal_error"
}

func revisionFromError(err error) uint64 {
	var ruleError holdem.RuleError
	if errors.As(err, &ruleError) && ruleError.Code == "stale_table_revision" {
		return 0
	}
	return 0
}

func (server *webSocketServer) lockUserRequests(userID string) func() {
	server.requestMu.Lock()
	lock := server.userLocks[userID]
	if lock == nil {
		lock = &sync.Mutex{}
		server.userLocks[userID] = lock
	}
	server.requestMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func isIdempotentRequest(messageType protocol.MessageType) bool {
	switch messageType {
	case protocol.TypeTableLeave,
		protocol.TypeTableReadySet,
		protocol.TypeTableActionSubmit,
		protocol.TypeTableHoleCardsReveal,
		protocol.TypeTableHoleCardsViewRequest,
		protocol.TypeTableHoleCardsViewRespond,
		protocol.TypeTableSeatChangeRequest,
		protocol.TypeTableSeatSwapRespond,
		protocol.TypeTableRunoutChoose,
		protocol.TypeTableTimeExtensionUse,
		protocol.TypeTableRebuy,
		protocol.TypeTableVoiceStateSet,
		protocol.TypeTableChatSend,
		protocol.TypeTablePlayerInteract:
		return true
	default:
		return false
	}
}
