package transport

import (
	"context"
	"time"
)

// 语音加入/退出元数据（ADR-001）：只持久化「谁、在哪个房间、何时进出语音频道」，
// 不记录任何音频内容。事件写入审计表，管理员可在审计查询界面按用户回看。
const (
	voiceEventJoined = "voice.joined"
	voiceEventLeft   = "voice.left"
)

// recordVoiceTransition 只在加入/退出状态真正变化时记录，开关麦克风不产生事件。
func (client *webSocketClient) recordVoiceTransition(roomID string, wasJoined, joined, microphoneEnabled bool) {
	switch {
	case !wasJoined && joined:
		client.recordVoiceEvent(roomID, voiceEventJoined, map[string]any{"microphoneEnabled": microphoneEnabled})
	case wasJoined && !joined:
		client.recordVoiceEvent(roomID, voiceEventLeft, map[string]any{"reason": "self"})
	}
}

func (client *webSocketClient) recordVoiceEvent(roomID, eventType string, metadata map[string]any) {
	if client.server.accounts == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.server.accounts.RecordRoomEvent(ctx, client.user.UserID, roomID, eventType, metadata); err != nil {
		client.server.logger.Warn("record voice event failed",
			"event", eventType, "userId", client.user.UserID, "roomId", roomID, "error", err)
	}
}
