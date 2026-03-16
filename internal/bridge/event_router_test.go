package bridge

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/n42/mautrix-wechat/internal/database"
	"github.com/n42/mautrix-wechat/pkg/wechat"
)

type testMatrixClient struct {
	redactions []testRedaction
	downloads  []string
	mediaData  []byte
	mediaType  string
}

type testRedaction struct {
	roomID  string
	eventID string
	reason  string
}

const testMessageMappingColumns = `wechat_msg_id, matrix_event_id, matrix_room_id, sender, msg_type, timestamp, created_at`

func (m *testMatrixClient) EnsureRegistered(_ context.Context, _ string) error  { return nil }
func (m *testMatrixClient) SetDisplayName(_ context.Context, _, _ string) error { return nil }
func (m *testMatrixClient) SetAvatarURL(_ context.Context, _, _ string) error   { return nil }
func (m *testMatrixClient) UploadMedia(_ context.Context, _ []byte, _, _ string) (string, error) {
	return "mxc://test/uploaded", nil
}
func (m *testMatrixClient) DownloadMedia(_ context.Context, mxcURI string) (io.ReadCloser, string, error) {
	m.downloads = append(m.downloads, mxcURI)
	data := m.mediaData
	if data == nil {
		data = []byte("media")
	}
	mimeType := m.mediaType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return io.NopCloser(bytes.NewReader(data)), mimeType, nil
}
func (m *testMatrixClient) SendMessage(_ context.Context, _, _ string, _ interface{}) (string, error) {
	return "$event:test", nil
}
func (m *testMatrixClient) SendMessageWithTimestamp(_ context.Context, _, _ string, _ interface{}, _ int64) (string, error) {
	return "$event:test", nil
}
func (m *testMatrixClient) CreateRoom(_ context.Context, _ *CreateRoomRequest) (string, error) {
	return "!room:test", nil
}
func (m *testMatrixClient) JoinRoom(_ context.Context, _, _ string) error        { return nil }
func (m *testMatrixClient) LeaveRoom(_ context.Context, _, _ string) error       { return nil }
func (m *testMatrixClient) InviteToRoom(_ context.Context, _, _ string) error    { return nil }
func (m *testMatrixClient) KickFromRoom(_ context.Context, _, _, _ string) error { return nil }
func (m *testMatrixClient) RedactEvent(_ context.Context, roomID, eventID, reason string) error {
	m.redactions = append(m.redactions, testRedaction{
		roomID:  roomID,
		eventID: eventID,
		reason:  reason,
	})
	return nil
}
func (m *testMatrixClient) SendStateEvent(_ context.Context, _, _, _ string, _ interface{}) error {
	return nil
}
func (m *testMatrixClient) SetRoomName(_ context.Context, _, _ string) error              { return nil }
func (m *testMatrixClient) SetRoomAvatar(_ context.Context, _, _ string) error            { return nil }
func (m *testMatrixClient) SetRoomTopic(_ context.Context, _, _ string) error             { return nil }
func (m *testMatrixClient) SetTyping(_ context.Context, _, _ string, _ bool, _ int) error { return nil }
func (m *testMatrixClient) SetPresence(_ context.Context, _ string, _ bool) error         { return nil }
func (m *testMatrixClient) SendReadReceipt(_ context.Context, _, _, _ string) error       { return nil }
func (m *testMatrixClient) CreateSpace(_ context.Context, _ *CreateSpaceRequest) (string, error) {
	return "!space:test", nil
}
func (m *testMatrixClient) AddRoomToSpace(_ context.Context, _, _ string) error { return nil }

func TestNewEventRouter_DefaultCrypto(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	if er.crypto == nil {
		t.Fatal("crypto should default to noopCryptoHelper")
	}

	// Verify it's a noop
	ctx := context.Background()
	if er.crypto.IsEncrypted(ctx, "!room:example.com") {
		t.Error("noop crypto should report rooms as unencrypted")
	}
}

func TestNewEventRouter_WithCrypto(t *testing.T) {
	pm := newTestPuppetManager()
	noop := &noopCryptoHelper{}
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
		Crypto:  noop,
	})

	if er.crypto != noop {
		t.Error("should use provided crypto helper")
	}
}

func TestEventRouter_SetProvider(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	if er.provider != nil {
		t.Error("provider should initially be nil")
	}

	mp := &mockProvider{name: "test", running: true}
	er.SetProvider(mp)

	if er.provider != mp {
		t.Error("SetProvider should update the provider")
	}
}

func TestEventRouter_HandleMatrixEvent_IgnorePuppet(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	evt := &MatrixEvent{
		Sender: "@wechat_wxid_test:example.com",
		Type:   "m.room.message",
		RoomID: "!room:example.com",
	}

	// Should return nil (ignored) since sender is a puppet
	err := er.HandleMatrixEvent(ctx, evt)
	if err != nil {
		t.Errorf("expected nil for puppet sender, got: %v", err)
	}
}

func TestEventRouter_HandleMatrixEvent_UnsupportedType(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
		Rooms:   nil, // will cause error for mapped rooms, but unsupported types are handled differently
	})

	ctx := context.Background()
	evt := &MatrixEvent{
		Sender: "@real_user:example.com",
		Type:   "m.custom.event",
		RoomID: "!room:example.com",
	}

	// HandleMatrixEvent first looks up the room, which requires rooms store.
	// Since rooms is nil, it will error at room lookup.
	err := er.HandleMatrixEvent(ctx, evt)
	if err == nil {
		t.Error("expected error when rooms store is nil")
	}
}

func TestEventRouter_OnMessage_NilDependencies(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	msg := &wechat.Message{
		MsgID:    "msg1",
		Type:     wechat.MsgText,
		FromUser: "wxid_sender",
		Content:  "hello",
	}

	// With nil db/bridgeUsers, should error gracefully without panic
	err := er.OnMessage(ctx, msg)
	if err == nil {
		t.Error("expected error when dependencies are nil")
	}
}

func TestEventRouter_OnLoginEvent(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	evt := &wechat.LoginEvent{
		State: wechat.LoginStateLoggedIn,
	}

	err := er.OnLoginEvent(ctx, evt)
	if err != nil {
		t.Errorf("OnLoginEvent should not error: %v", err)
	}
}

func TestEventRouter_OnLoginEvent_NilEvent(t *testing.T) {
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: newTestPuppetManager(),
	})

	err := er.OnLoginEvent(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil login event")
	}
}

func TestEventRouter_OnPresence_NilMatrixClient(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	err := er.OnPresence(ctx, "wxid_test", true)
	if err != nil {
		t.Errorf("OnPresence with nil matrixClient should return nil: %v", err)
	}
}

func TestEventRouter_OnTyping_NilMatrixClient(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	err := er.OnTyping(ctx, "wxid_test", "wxid_chat")
	if err != nil {
		t.Errorf("OnTyping with nil matrixClient should return nil: %v", err)
	}
}

func TestEventRouter_OnRevoke_NilMatrixClient(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	err := er.OnRevoke(ctx, "msg1", "message revoked")
	if err != nil {
		t.Errorf("OnRevoke with nil matrixClient should return nil: %v", err)
	}
}

func TestEventRouter_OnRevoke_NilMessageStore(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:          slog.Default(),
		Puppets:      pm,
		MatrixClient: &testMatrixClient{},
	})

	err := er.OnRevoke(context.Background(), "msg1", "message revoked")
	if err != nil {
		t.Fatalf("OnRevoke should tolerate nil message store: %v", err)
	}
}

func TestEventRouter_OnRevoke_RedactsLatestMappedEvent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ` + testMessageMappingColumns + ` FROM message_mapping WHERE wechat_msg_id = $1 ORDER BY created_at DESC LIMIT 1`)).
		WithArgs("msg1").
		WillReturnRows(sqlmock.NewRows([]string{
			"wechat_msg_id", "matrix_event_id", "matrix_room_id", "sender", "msg_type", "timestamp", "created_at",
		}).AddRow("msg1", "$event:test", "!room:test", "@user:test", 1, now, now))

	matrix := &testMatrixClient{}
	er := NewEventRouter(EventRouterConfig{
		Log:          slog.Default(),
		Puppets:      newTestPuppetManager(),
		MatrixClient: matrix,
		Messages:     database.NewMessageMappingStore(db),
	})

	if err := er.OnRevoke(context.Background(), "msg1", "recalled"); err != nil {
		t.Fatalf("OnRevoke error: %v", err)
	}

	if len(matrix.redactions) != 1 {
		t.Fatalf("expected 1 redaction, got %d", len(matrix.redactions))
	}
	if matrix.redactions[0].roomID != "!room:test" || matrix.redactions[0].eventID != "$event:test" {
		t.Fatalf("unexpected redaction payload: %+v", matrix.redactions[0])
	}
	if matrix.redactions[0].reason != "recalled" {
		t.Fatalf("unexpected redaction reason: %s", matrix.redactions[0].reason)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestEventRouter_HandleMatrixMessage_ForwardsMedia(t *testing.T) {
	matrix := &testMatrixClient{
		mediaData: []byte("payload"),
		mediaType: "image/png",
	}
	provider := newMockProvider("padpro", 2)
	er := NewEventRouter(EventRouterConfig{
		Log:          slog.Default(),
		Puppets:      newTestPuppetManager(),
		Processor:    &defaultMessageProcessor{},
		Provider:     provider,
		MatrixClient: matrix,
	})

	room := &database.RoomMapping{
		WeChatChatID: "wxid_chat",
		MatrixRoomID: "!room:test",
	}

	tests := []struct {
		name    string
		content map[string]interface{}
	}{
		{
			name: "image",
			content: map[string]interface{}{
				"msgtype": "m.image",
				"body":    "image.png",
				"url":     "mxc://test/image",
			},
		},
		{
			name: "file",
			content: map[string]interface{}{
				"msgtype": "m.file",
				"body":    "report.pdf",
				"url":     "mxc://test/file",
			},
		},
		{
			name: "video",
			content: map[string]interface{}{
				"msgtype": "m.video",
				"body":    "clip.mp4",
				"url":     "mxc://test/video",
				"info": map[string]interface{}{
					"thumbnail_url": "mxc://test/video-thumb",
				},
			},
		},
		{
			name: "voice",
			content: map[string]interface{}{
				"msgtype": "m.audio",
				"body":    "voice.ogg",
				"url":     "mxc://test/voice",
				"info": map[string]interface{}{
					"duration": float64(2300),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beforeDownloads := len(matrix.downloads)

			err := er.handleMatrixMessage(context.Background(), &MatrixEvent{
				ID:      "$event:test",
				Type:    "m.room.message",
				RoomID:  room.MatrixRoomID,
				Sender:  "@user:test",
				Content: tt.content,
			}, room)
			if err != nil {
				t.Fatalf("handleMatrixMessage: %v", err)
			}
			newDownloads := matrix.downloads[beforeDownloads:]
			if len(newDownloads) == 0 {
				t.Fatal("expected media download")
			}
			if newDownloads[0] != tt.content["url"] {
				t.Fatalf("downloaded %s, want %v", newDownloads[0], tt.content["url"])
			}
			switch tt.name {
			case "image":
				if len(provider.sentImages) != 1 {
					t.Fatalf("expected 1 sent image, got %d", len(provider.sentImages))
				}
				if provider.sentImages[0].filename != "image.png" || string(provider.sentImages[0].data) != "payload" {
					t.Fatalf("unexpected image payload: %+v", provider.sentImages[0])
				}
			case "file":
				if len(provider.sentFiles) != 1 {
					t.Fatalf("expected 1 sent file, got %d", len(provider.sentFiles))
				}
				if provider.sentFiles[0].filename != "report.pdf" || string(provider.sentFiles[0].data) != "payload" {
					t.Fatalf("unexpected file payload: %+v", provider.sentFiles[0])
				}
			case "video":
				if len(provider.sentVideos) != 1 {
					t.Fatalf("expected 1 sent video, got %d", len(provider.sentVideos))
				}
				if provider.sentVideos[0].filename != "clip.mp4" || string(provider.sentVideos[0].data) != "payload" {
					t.Fatalf("unexpected video payload: %+v", provider.sentVideos[0])
				}
				if string(provider.sentVideos[0].thumbnail) != "payload" {
					t.Fatalf("unexpected video thumbnail: %+v", provider.sentVideos[0])
				}
				if len(newDownloads) != 2 || newDownloads[0] != "mxc://test/video" || newDownloads[1] != "mxc://test/video-thumb" {
					t.Fatalf("unexpected video downloads: %+v", newDownloads)
				}
			case "voice":
				if len(provider.sentVoices) != 1 {
					t.Fatalf("expected 1 sent voice, got %d", len(provider.sentVoices))
				}
				if string(provider.sentVoices[0].data) != "payload" || provider.sentVoices[0].duration != 3 {
					t.Fatalf("unexpected voice payload: %+v", provider.sentVoices[0])
				}
			}
		})
	}
}

func TestEventRouter_BackfillRoom_Empty(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	err := er.BackfillRoom(ctx, nil, nil)
	if err != nil {
		t.Errorf("BackfillRoom with nil messages should return nil: %v", err)
	}
}

func TestEventRouter_BackfillRoom_NilProcessor(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	msgs := []*wechat.Message{{MsgID: "m1"}}
	err := er.BackfillRoom(ctx, nil, msgs)
	if err == nil {
		t.Error("BackfillRoom with nil processor should return error")
	}
}

func TestEventRouter_BackfillRoom_NilMessageStore(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:          slog.Default(),
		Puppets:      pm,
		Processor:    &defaultMessageProcessor{},
		MatrixClient: &testMatrixClient{},
	})

	msgs := []*wechat.Message{{MsgID: "m1", Type: wechat.MsgText, Content: "hello", FromUser: "wxid1"}}
	err := er.BackfillRoom(context.Background(), &database.RoomMapping{MatrixRoomID: "!room:test"}, msgs)
	if err == nil {
		t.Fatal("expected error when message store is nil")
	}
}

func TestEventRouter_OnGroupMemberUpdate_NilFields(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	// bridgeUsers is nil, so findBridgeUser will fail early
	err := er.OnGroupMemberUpdate(ctx, "group1", nil)
	if err != nil {
		t.Errorf("should return nil with nil bridgeUsers: %v", err)
	}
}

func TestEventRouter_SyncPuppetAvatar_NilMatrixClient(t *testing.T) {
	er := NewEventRouter(EventRouterConfig{
		Log:      slog.Default(),
		Puppets:  newTestPuppetManager(),
		Provider: newMockProvider("padpro", 2),
	})

	er.syncPuppetAvatar(context.Background(), &Puppet{MatrixUserID: "@wechat_wxid:example.com"}, &wechat.ContactInfo{
		UserID:    "wxid_test",
		AvatarURL: "https://example.com/avatar.jpg",
	})
}

func TestEventRouter_MetricsRecording(t *testing.T) {
	pm := newTestPuppetManager()
	metrics := NewMetrics()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
		Metrics: metrics,
	})

	// Verify metrics reference is stored
	if er.metrics != metrics {
		t.Error("metrics should be stored in EventRouter")
	}
}
