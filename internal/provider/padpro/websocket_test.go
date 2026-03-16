package padpro

import (
	"log/slog"
	"testing"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

func TestWSClient_WebsocketURL(t *testing.T) {
	ws := newWSClient("wss://padpro.example.com:1239", "secret-key", nil, slog.Default())

	got := ws.websocketURL()
	want := "wss://padpro.example.com:1239/ws/GetSyncMsg?key=secret-key"
	if got != want {
		t.Fatalf("websocketURL = %s, want %s", got, want)
	}
}

func TestWSClient_DispatchMessage_NoHandler(t *testing.T) {
	ws := newWSClient("wss://padpro.example.com:1239", "secret-key", nil, slog.Default())

	ws.dispatchMessage(wsMessage{
		NewMsgID:     123,
		MsgType:      int(wechat.MsgText),
		FromUserName: strField{Str: "wxid_sender"},
		Content:      strField{Str: "hello"},
	})
}
