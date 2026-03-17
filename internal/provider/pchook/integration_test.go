package pchook

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

type rpcRawRequest struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcTestServer struct {
	t             *testing.T
	listener      net.Listener
	selfPayload   []byte
	avatarPath    string
	downloadPath  string
	mu            sync.Mutex
	calls         []string
	savedFileSeen []string
}

func newRPCTestServer(t *testing.T, selfPayload []byte, avatarPath, downloadPath string) *rpcTestServer {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	s := &rpcTestServer{
		t:            t,
		listener:     listener,
		selfPayload:  selfPayload,
		avatarPath:   avatarPath,
		downloadPath: downloadPath,
	}
	go s.acceptLoop()
	return s
}

func (s *rpcTestServer) endpoint() string {
	return s.listener.Addr().String()
}

func (s *rpcTestServer) close() {
	_ = s.listener.Close()
}

func (s *rpcTestServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *rpcTestServer) handleConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}

		var req rpcRawRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}

		s.recordCall(req.Method)
		resp := map[string]interface{}{"id": req.ID}

		switch req.Method {
		case "ping":
			resp["result"] = "pong"
		case "get_self_info":
			var payload interface{}
			if len(s.selfPayload) > 0 {
				if err := json.Unmarshal(s.selfPayload, &payload); err != nil {
					s.t.Fatalf("unmarshal self payload: %v", err)
				}
			} else {
				payload = map[string]interface{}{}
			}
			resp["result"] = payload
		case "send_text":
			resp["result"] = "msg_text"
		case "send_image":
			var params sendImageParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				s.t.Fatalf("unmarshal send_image params: %v", err)
			}
			s.mustSeeFile(params.Path)
			resp["result"] = "msg_image"
		case "send_file":
			var params sendFileParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				s.t.Fatalf("unmarshal send_file params: %v", err)
			}
			s.mustSeeFile(params.Path)
			resp["result"] = "msg_file"
		case "revoke_msg", "accept_friend", "set_remark", "invite_to_group", "remove_from_group", "set_group_name", "set_group_announcement", "leave_group":
			resp["result"] = true
		case "get_contacts":
			resp["result"] = []contactResult{
				{UserID: "wxid_friend", Nickname: "Friend"},
				{UserID: "group@chatroom", Nickname: "Group Chat"},
			}
		case "get_contact_info":
			resp["result"] = contactResult{UserID: "wxid_friend", Nickname: "Friend"}
		case "get_avatar":
			resp["result"] = s.avatarPath
		case "get_group_members":
			resp["result"] = []groupMemberResult{
				{UserID: "wxid_friend", Nickname: "Friend", DisplayName: "Friendly", IsAdmin: true},
			}
		case "create_group":
			resp["result"] = "group@chatroom"
		case "download_media":
			resp["result"] = s.downloadPath
		default:
			resp["result"] = true
		}

		data, err := json.Marshal(resp)
		if err != nil {
			s.t.Fatalf("marshal response: %v", err)
		}
		if _, err := conn.Write(append(data, '\n')); err != nil {
			return
		}
	}
}

func (s *rpcTestServer) recordCall(method string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, method)
}

func (s *rpcTestServer) mustSeeFile(path string) {
	s.t.Helper()
	if _, err := os.Stat(path); err != nil {
		s.t.Fatalf("expected temp file to exist: %v", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.savedFileSeen = append(s.savedFileSeen, path)
}

func (s *rpcTestServer) sawCall(method string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, call := range s.calls {
		if call == method {
			return true
		}
	}
	return false
}

type recordingHandler struct {
	mu            sync.Mutex
	messages      []*wechat.Message
	loginEvents   []*wechat.LoginEvent
	contacts      []*wechat.ContactInfo
	revokedMsgIDs []string
}

func (h *recordingHandler) OnMessage(ctx context.Context, msg *wechat.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.messages = append(h.messages, msg)
	return nil
}

func (h *recordingHandler) OnLoginEvent(ctx context.Context, evt *wechat.LoginEvent) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.loginEvents = append(h.loginEvents, evt)
	return nil
}

func (h *recordingHandler) OnContactUpdate(ctx context.Context, contact *wechat.ContactInfo) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.contacts = append(h.contacts, contact)
	return nil
}

func (h *recordingHandler) OnGroupMemberUpdate(ctx context.Context, groupID string, members []*wechat.GroupMember) error {
	return nil
}

func (h *recordingHandler) OnPresence(ctx context.Context, userID string, online bool) error {
	return nil
}

func (h *recordingHandler) OnTyping(ctx context.Context, userID string, chatID string) error {
	return nil
}

func (h *recordingHandler) OnRevoke(ctx context.Context, msgID string, replaceTip string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.revokedMsgIDs = append(h.revokedMsgIDs, msgID)
	return nil
}

func TestProviderRPCBackedLifecycleAndOperations(t *testing.T) {
	tempDir := t.TempDir()
	avatarPath := filepath.Join(tempDir, "avatar.png")
	downloadPath := filepath.Join(tempDir, "download.png")
	directMediaPath := filepath.Join(tempDir, "direct.jpg")

	if err := os.WriteFile(avatarPath, []byte("avatar"), 0o600); err != nil {
		t.Fatalf("write avatar: %v", err)
	}
	if err := os.WriteFile(downloadPath, []byte("download"), 0o600); err != nil {
		t.Fatalf("write download media: %v", err)
	}
	if err := os.WriteFile(directMediaPath, []byte("direct"), 0o600); err != nil {
		t.Fatalf("write direct media: %v", err)
	}

	selfPayload, err := json.Marshal(contactResult{
		UserID:   "wxid_self",
		Nickname: "Bridge Bot",
	})
	if err != nil {
		t.Fatalf("marshal self payload: %v", err)
	}

	server := newRPCTestServer(t, selfPayload, avatarPath, downloadPath)
	defer server.close()

	handler := &recordingHandler{}
	p := &Provider{}
	cfg := &wechat.ProviderConfig{
		DataDir: tempDir,
		Extra:   map[string]string{"rpc_endpoint": server.endpoint()},
	}

	if err := p.Init(cfg, handler); err != nil {
		t.Fatalf("init: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		if err := p.Stop(); err != nil {
			t.Fatalf("stop: %v", err)
		}
	}()

	if !p.IsRunning() {
		t.Fatal("provider should be running")
	}
	if p.GetLoginState() != wechat.LoginStateLoggedIn {
		t.Fatalf("login state: %v", p.GetLoginState())
	}
	if self := p.GetSelf(); self == nil || self.UserID != "wxid_self" {
		t.Fatalf("unexpected self: %+v", self)
	}
	if p.GetRPCClient() == nil {
		t.Fatal("rpc client should be set")
	}

	msgID, err := p.SendText(ctx, "wxid_friend", "hello")
	if err != nil || msgID != "msg_text" {
		t.Fatalf("send text: %v, msg=%s", err, msgID)
	}

	imageID, err := p.SendImage(ctx, "wxid_friend", bytes.NewBufferString("image"), "photo.jpg")
	if err != nil || imageID != "msg_image" {
		t.Fatalf("send image: %v, msg=%s", err, imageID)
	}

	fileID, err := p.SendFile(ctx, "wxid_friend", bytes.NewBufferString("file"), "doc.pdf")
	if err != nil || fileID != "msg_file" {
		t.Fatalf("send file: %v, msg=%s", err, fileID)
	}

	if _, err := p.SendImageFromPath(ctx, "wxid_friend", directMediaPath); err != nil {
		t.Fatalf("send image from path: %v", err)
	}
	if _, err := p.SendFileFromPath(ctx, "wxid_friend", directMediaPath); err != nil {
		t.Fatalf("send file from path: %v", err)
	}
	if err := p.RevokeMessage(ctx, "msg_text", "wxid_friend"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := p.AcceptFriendRequest(ctx, "<xml/>"); err != nil {
		t.Fatalf("accept friend: %v", err)
	}
	if err := p.SetContactRemark(ctx, "wxid_friend", "pal"); err != nil {
		t.Fatalf("set remark: %v", err)
	}

	contacts, err := p.GetContactList(ctx)
	if err != nil || len(contacts) != 2 {
		t.Fatalf("contacts: %v len=%d", err, len(contacts))
	}
	groups, err := p.GetGroupList(ctx)
	if err != nil || len(groups) != 1 || !groups[0].IsGroup {
		t.Fatalf("groups: %v len=%d", err, len(groups))
	}
	contact, err := p.GetContactInfo(ctx, "wxid_friend")
	if err != nil || contact.UserID != "wxid_friend" {
		t.Fatalf("contact info: %v %+v", err, contact)
	}

	avatarData, avatarMime, err := p.GetUserAvatar(ctx, "wxid_friend")
	if err != nil || string(avatarData) != "avatar" || avatarMime != "image/png" {
		t.Fatalf("avatar: %v data=%q mime=%s", err, string(avatarData), avatarMime)
	}

	members, err := p.GetGroupMembers(ctx, "group@chatroom")
	if err != nil || len(members) != 1 || members[0].UserID != "wxid_friend" {
		t.Fatalf("group members: %v %+v", err, members)
	}
	groupInfo, err := p.GetGroupInfo(ctx, "group@chatroom")
	if err != nil || groupInfo == nil || groupInfo.UserID != "wxid_friend" {
		t.Fatalf("group info: %v %+v", err, groupInfo)
	}
	groupID, err := p.CreateGroup(ctx, "New Group", []string{"wxid_friend"})
	if err != nil || groupID != "group@chatroom" {
		t.Fatalf("create group: %v group=%s", err, groupID)
	}
	if err := p.InviteToGroup(ctx, groupID, []string{"wxid_friend"}); err != nil {
		t.Fatalf("invite: %v", err)
	}
	if err := p.RemoveFromGroup(ctx, groupID, []string{"wxid_friend"}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := p.SetGroupName(ctx, groupID, "Renamed"); err != nil {
		t.Fatalf("set group name: %v", err)
	}
	if err := p.SetGroupAnnouncement(ctx, groupID, "hello"); err != nil {
		t.Fatalf("set group announcement: %v", err)
	}
	if err := p.LeaveGroup(ctx, groupID); err != nil {
		t.Fatalf("leave group: %v", err)
	}

	reader, mimeType, err := p.DownloadMedia(ctx, &wechat.Message{
		MsgID: "msg-direct",
		Extra: map[string]string{"media_path": directMediaPath},
	})
	if err != nil {
		t.Fatalf("download direct media: %v", err)
	}
	data, err := os.ReadFile(directMediaPath)
	if err != nil {
		t.Fatalf("read direct media: %v", err)
	}
	readBack, err := readAllAndClose(reader)
	if err != nil || string(readBack) != string(data) || mimeType != "image/jpeg" {
		t.Fatalf("direct media: %v data=%q mime=%s", err, string(readBack), mimeType)
	}

	downloaded, downloadedMime, err := p.DownloadMediaToBytes(ctx, &wechat.Message{
		MsgID: "msg-download",
		Extra: map[string]string{},
	})
	if err != nil || string(downloaded) != "download" || downloadedMime != "image/png" {
		t.Fatalf("download fallback: %v data=%q mime=%s", err, string(downloaded), downloadedMime)
	}

	if _, err := p.saveToTempFromBytes([]byte("bytes"), "bytes.txt"); err != nil {
		t.Fatalf("save bytes: %v", err)
	}

	for _, method := range []string{"ping", "get_self_info", "send_text", "send_image", "send_file", "get_contacts", "download_media"} {
		if !server.sawCall(method) {
			t.Fatalf("expected RPC method %q to be called", method)
		}
	}

	if err := p.Logout(ctx); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if p.GetLoginState() != wechat.LoginStateLoggedOut {
		t.Fatalf("login state after logout: %v", p.GetLoginState())
	}
}

func TestProviderLoginEmitsErrorWhenNoDesktopSession(t *testing.T) {
	server := newRPCTestServer(t, nil, "", "")
	defer server.close()

	handler := &recordingHandler{}
	p := &Provider{}
	if err := p.Init(&wechat.ProviderConfig{
		DataDir: t.TempDir(),
		Extra:   map[string]string{"rpc_endpoint": server.endpoint()},
	}, handler); err != nil {
		t.Fatalf("init: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.rpc.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer p.rpc.Close()

	if err := p.Login(ctx); err == nil {
		t.Fatal("login should fail when no desktop session is present")
	}
	if len(handler.loginEvents) != 1 || handler.loginEvents[0].State != wechat.LoginStateError {
		t.Fatalf("unexpected login events: %+v", handler.loginEvents)
	}
}

func TestProviderHandleNotificationDispatchesCallbacks(t *testing.T) {
	handler := &recordingHandler{}
	p := &Provider{
		handler: handler,
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	messageData := json.RawMessage(`{"msg_id":"msg1","type":1,"sender":"wxid_sender","content":"hello"}`)
	p.handleNotification("on_message", messageData)

	contactData := json.RawMessage(`{"wxid":"wxid_friend","nickname":"Friend"}`)
	p.handleNotification("on_contact_update", contactData)

	revokeData := json.RawMessage(`{"msg_id":"msg1","replace_tip":"revoked"}`)
	p.handleNotification("on_revoke", revokeData)

	p.handleNotification("on_message", json.RawMessage(`{"bad":`))
	p.handleNotification("unknown", json.RawMessage(`{}`))

	if len(handler.messages) != 1 || handler.messages[0].MsgID != "msg1" {
		t.Fatalf("unexpected messages: %+v", handler.messages)
	}
	if len(handler.contacts) != 1 || handler.contacts[0].UserID != "wxid_friend" {
		t.Fatalf("unexpected contacts: %+v", handler.contacts)
	}
	if len(handler.revokedMsgIDs) != 1 || handler.revokedMsgIDs[0] != "msg1" {
		t.Fatalf("unexpected revoked ids: %+v", handler.revokedMsgIDs)
	}
}

func readAllAndClose(rc io.ReadCloser) ([]byte, error) {
	defer rc.Close()
	return io.ReadAll(rc)
}
