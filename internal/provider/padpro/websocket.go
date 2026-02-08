package padpro

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/gorilla/websocket"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// wsClient manages the WebSocket connection to WeChatPadPro for real-time message sync.
//
// WeChatPadPro uses the endpoint: ws://<host>/ws/GetSyncMsg?key=<auth_key>
// Messages are JSON-encoded wsMessage structs pushed from the server.
type wsClient struct {
	endpoint string
	authKey  string
	handler  wechat.MessageHandler
	log      *slog.Logger
	conn     *websocket.Conn
}

func newWSClient(endpoint, authKey string, handler wechat.MessageHandler, log *slog.Logger) *wsClient {
	return &wsClient{
		endpoint: endpoint,
		authKey:  authKey,
		handler:  handler,
		log:      log,
	}
}

// connect establishes the WebSocket connection and enters the read loop.
// It blocks until the connection is lost or stopCh is closed.
func (ws *wsClient) connect(stopCh chan struct{}) error {
	wsURL := fmt.Sprintf("%s/ws/GetSyncMsg?key=%s", ws.endpoint, ws.authKey)
	ws.log.Info("connecting to WebSocket", "url", wsURL)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}
	ws.conn = conn
	ws.log.Info("WebSocket connected")

	return ws.readLoop(stopCh)
}

// readLoop continuously reads messages from the WebSocket connection.
func (ws *wsClient) readLoop(stopCh chan struct{}) error {
	defer ws.conn.Close()

	for {
		select {
		case <-stopCh:
			return nil
		default:
		}

		// Set read deadline to detect dead connections
		ws.conn.SetReadDeadline(time.Now().Add(90 * time.Second))

		_, data, err := ws.conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("websocket read: %w", err)
		}

		ws.log.Debug("ws message received", "size", len(data))

		var raw wsMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			ws.log.Warn("failed to parse ws message", "error", err, "data", string(data))
			continue
		}

		ws.dispatchMessage(raw)
	}
}

// dispatchMessage routes a parsed WebSocket message to the appropriate handler.
func (ws *wsClient) dispatchMessage(raw wsMessage) {
	ctx := context.Background()

	msgType := wechat.MsgType(raw.MsgType)

	switch msgType {
	case wechat.MsgRevoke:
		ws.handleRevoke(ctx, raw)
	case wechat.MsgSystem:
		ws.log.Debug("system message", "content", raw.Content.Str, "from", raw.FromUserName.Str)
	default:
		msg := convertWSMessage(raw)
		if msg == nil {
			return
		}
		if err := ws.handler.OnMessage(ctx, msg); err != nil {
			ws.log.Error("handle message failed", "error", err, "msg_id", msg.MsgID)
		}
	}
}

func (ws *wsClient) handleRevoke(ctx context.Context, raw wsMessage) {
	msg := convertWSMessage(raw)
	if msg == nil {
		return
	}
	if err := ws.handler.OnRevoke(ctx, msg.MsgID, raw.PushContent); err != nil {
		ws.log.Error("handle revoke failed", "error", err, "msg_id", msg.MsgID)
	}
}

// close terminates the WebSocket connection.
func (ws *wsClient) close() error {
	if ws.conn != nil {
		return ws.conn.Close()
	}
	return nil
}
