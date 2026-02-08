package padpro

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// WebhookHandler processes incoming webhook callbacks from WeChatPadPro.
// WeChatPadPro can be configured to POST messages to a webhook URL
// as an alternative (or supplement) to WebSocket message sync.
//
// Configure via: POST /v1/webhook/Config {"url":"http://bridge:29353/callback","enabled":true}
type WebhookHandler struct {
	log     *slog.Logger
	handler wechat.MessageHandler
}

// NewWebhookHandler creates a new webhook callback handler.
func NewWebhookHandler(log *slog.Logger, handler wechat.MessageHandler) *WebhookHandler {
	return &WebhookHandler{
		log:     log,
		handler: handler,
	}
}

// ServeHTTP implements http.Handler for the webhook callback endpoint.
func (wh *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var raw wsMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		wh.log.Warn("invalid webhook payload", "error", err)
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	wh.dispatch(ctx, raw)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"ret":0}`)
}

// dispatch routes a webhook message to the appropriate handler.
func (wh *WebhookHandler) dispatch(ctx context.Context, raw wsMessage) {
	msgType := wechat.MsgType(raw.MsgType)

	switch msgType {
	case wechat.MsgRevoke:
		msg := convertWSMessage(raw)
		if msg == nil {
			return
		}
		if err := wh.handler.OnRevoke(ctx, msg.MsgID, raw.PushContent); err != nil {
			wh.log.Error("handle revoke failed", "error", err)
		}
	case wechat.MsgSystem:
		wh.log.Debug("system message via webhook", "content", raw.Content.Str)
	default:
		msg := convertWSMessage(raw)
		if msg == nil {
			wh.log.Warn("webhook message missing from_user_name")
			return
		}
		if err := wh.handler.OnMessage(ctx, msg); err != nil {
			wh.log.Error("handle message failed", "error", err, "msg_id", msg.MsgID)
		}
	}
}
