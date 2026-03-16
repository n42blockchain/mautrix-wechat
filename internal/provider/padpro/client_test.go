package padpro

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientBuildURLAndEncodeMedia(t *testing.T) {
	c := NewClient("https://padpro.example.com/api?foo=bar", "secret")
	got := c.buildURL("/message/SendTextMessage")
	if !strings.Contains(got, "key=secret") || !strings.Contains(got, "foo=bar") {
		t.Fatalf("buildURL = %s", got)
	}

	encoded, err := EncodeMediaToBase64(strings.NewReader("abc"))
	if err != nil {
		t.Fatalf("EncodeMediaToBase64 error: %v", err)
	}
	if encoded != "YWJj" {
		t.Fatalf("encoded = %s", encoded)
	}
}

func TestClientGetAndPostJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			if r.URL.Query().Get("key") != "secret" {
				t.Fatalf("missing key query: %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"code":0,"msg":"ok","data":{"value":"x"}}`)
		case "/post":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method %s", r.Method)
			}
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Fatalf("content-type = %s", ct)
			}
			_, _ = io.WriteString(w, `{"code":200,"msg":"ok","data":{"status":"done"}}`)
		case "/http-error":
			http.Error(w, "bad upstream", http.StatusBadGateway)
		case "/api-error":
			_, _ = io.WriteString(w, `{"code":500,"msg":"failed"}`)
		case "/bad-json":
			_, _ = io.WriteString(w, `not-json`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "secret")

	resp, err := c.Get(context.Background(), "/ok")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := c.ParseData(resp, &out); err != nil {
		t.Fatalf("ParseData error: %v", err)
	}
	if out.Value != "x" {
		t.Fatalf("out.Value = %s", out.Value)
	}

	if _, err := c.PostJSON(context.Background(), "/post", map[string]string{"a": "b"}); err != nil {
		t.Fatalf("PostJSON error: %v", err)
	}
	if _, err := c.Get(context.Background(), "/http-error"); err == nil {
		t.Fatal("expected HTTP error")
	}
	if _, err := c.Get(context.Background(), "/api-error"); err == nil {
		t.Fatal("expected API error")
	}
	if _, err := c.Get(context.Background(), "/bad-json"); err == nil {
		t.Fatal("expected parse error")
	}
	if err := c.ParseData(&apiResponse{}, &out); err == nil {
		t.Fatal("expected no data error")
	}
}

func TestClientAPIWrappers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login/GetLoginQrCodeNew":
			_, _ = io.WriteString(w, `{"code":0,"data":{"qr_code":"YWJj","qr_url":"https://example.com/qr","uuid":"u1"}}`)
		case "/login/CheckLoginStatus":
			_, _ = io.WriteString(w, `{"code":0,"data":{"status":2,"user_name":"wxid_user","nick_name":"Alice","head_url":"https://example.com/a.jpg"}}`)
		case "/login/LogOut":
			_, _ = io.WriteString(w, `{"code":0,"data":{}}`)
		case "/message/SendTextMessage", "/message/SendImageMessage", "/message/SendVoice", "/message/CdnUploadVideo", "/message/sendFile":
			_, _ = io.WriteString(w, `{"code":0,"data":{"msg_id":11,"new_msg_id":22}}`)
		case "/message/RevokeMsg":
			_, _ = io.WriteString(w, `{"code":0,"data":{}}`)
		case "/friend/GetFriendList":
			_, _ = io.WriteString(w, `{"code":0,"data":{"friends":["wxid1","wxid2"]}}`)
		case "/friend/GetContactDetailsList":
			_, _ = io.WriteString(w, `{"code":0,"data":{"contacts":[{"user_name":{"str":"wxid1"},"nick_name":{"str":"Alice"}}]}}`)
		case "/friend/AgreeAdd", "/friend/SetRemark":
			_, _ = io.WriteString(w, `{"code":0,"data":{}}`)
		case "/group/GetChatRoomInfo":
			_, _ = io.WriteString(w, `{"code":0,"data":{"chat_room_name":{"str":"group@chatroom"},"nick_name":{"str":"Group"},"member_count":1,"members":[]}}`)
		case "/group/CreateChatRoom":
			_, _ = io.WriteString(w, `{"code":0,"data":{"chat_room_name":"group@chatroom"}}`)
		case "/group/AddChatRoomMembers", "/group/DelChatRoomMembers", "/group/SetChatroomName", "/group/SetChatroomAnnouncement", "/group/QuitChatRoom":
			_, _ = io.WriteString(w, `{"code":0,"data":{}}`)
		case "/v1/webhook/Config":
			_, _ = io.WriteString(w, `{"code":0,"data":{}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "secret")
	ctx := context.Background()

	qr, err := c.GetLoginQRCode(ctx)
	if err != nil || qr == nil || qr.UUID != "u1" {
		t.Fatalf("GetLoginQRCode error=%v qr=%+v", err, qr)
	}
	status, err := c.CheckLoginStatus(ctx)
	if err != nil || status == nil || status.UserName != "wxid_user" {
		t.Fatalf("CheckLoginStatus error=%v status=%+v", err, status)
	}
	if err := c.Logout(ctx); err != nil {
		t.Fatalf("Logout error: %v", err)
	}
	if _, err := c.SendTextMessage(ctx, &sendTextRequest{}); err != nil {
		t.Fatalf("SendTextMessage error: %v", err)
	}
	if _, err := c.SendImageMessage(ctx, &sendImageRequest{}); err != nil {
		t.Fatalf("SendImageMessage error: %v", err)
	}
	if _, err := c.SendVoice(ctx, &sendVoiceRequest{}); err != nil {
		t.Fatalf("SendVoice error: %v", err)
	}
	if _, err := c.CdnUploadVideo(ctx, &sendVideoRequest{}); err != nil {
		t.Fatalf("CdnUploadVideo error: %v", err)
	}
	if _, err := c.SendFile(ctx, &sendFileRequest{}); err != nil {
		t.Fatalf("SendFile error: %v", err)
	}
	if err := c.RevokeMsg(ctx, &revokeRequest{}); err != nil {
		t.Fatalf("RevokeMsg error: %v", err)
	}
	friends, err := c.GetFriendList(ctx)
	if err != nil || len(friends) != 2 {
		t.Fatalf("GetFriendList error=%v friends=%v", err, friends)
	}
	contacts, err := c.GetContactDetailsList(ctx, []string{"wxid1"})
	if err != nil || len(contacts) != 1 {
		t.Fatalf("GetContactDetailsList error=%v contacts=%v", err, contacts)
	}
	if err := c.AgreeAdd(ctx, "enc", "ticket", 1); err != nil {
		t.Fatalf("AgreeAdd error: %v", err)
	}
	if err := c.SetRemark(ctx, "wxid1", "remark"); err != nil {
		t.Fatalf("SetRemark error: %v", err)
	}
	groupInfo, err := c.GetChatRoomInfo(ctx, "group@chatroom")
	if err != nil || groupInfo == nil || groupInfo.ChatRoomName.Str != "group@chatroom" {
		t.Fatalf("GetChatRoomInfo error=%v group=%+v", err, groupInfo)
	}
	group, err := c.CreateChatRoom(ctx, []string{"wxid1"})
	if err != nil || group == nil || group.ChatRoomName != "group@chatroom" {
		t.Fatalf("CreateChatRoom error=%v group=%+v", err, group)
	}
	if err := c.AddChatRoomMembers(ctx, "group@chatroom", []string{"wxid1"}); err != nil {
		t.Fatalf("AddChatRoomMembers error: %v", err)
	}
	if err := c.DelChatRoomMembers(ctx, "group@chatroom", []string{"wxid1"}); err != nil {
		t.Fatalf("DelChatRoomMembers error: %v", err)
	}
	if err := c.SetChatroomName(ctx, "group@chatroom", "Group"); err != nil {
		t.Fatalf("SetChatroomName error: %v", err)
	}
	if err := c.SetChatroomAnnouncement(ctx, "group@chatroom", "Notice"); err != nil {
		t.Fatalf("SetChatroomAnnouncement error: %v", err)
	}
	if err := c.QuitChatRoom(ctx, "group@chatroom"); err != nil {
		t.Fatalf("QuitChatRoom error: %v", err)
	}
	if err := c.ConfigureWebhook(ctx, "https://example.com/callback"); err != nil {
		t.Fatalf("ConfigureWebhook error: %v", err)
	}
}
