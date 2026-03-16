package padpro

import "testing"

func TestConvertWSMessage_GroupIncoming(t *testing.T) {
	msg := convertWSMessage(wsMessage{
		NewMsgID:     99,
		MsgType:      1,
		FromUserName: strField{Str: "group@chatroom"},
		ToUserName:   strField{Str: "wxid_me"},
		Content:      strField{Str: "wxid_sender:\nhello group"},
		CreateTime:   123,
		MsgSource:    "src",
		PushContent:  "push",
		MsgID:        11,
	})
	if msg == nil {
		t.Fatal("expected message")
	}
	if !msg.IsGroup || msg.GroupID != "group@chatroom" {
		t.Fatalf("group flags wrong: %+v", msg)
	}
	if msg.FromUser != "wxid_sender" || msg.Content != "hello group" {
		t.Fatalf("sender/content wrong: %+v", msg)
	}
	if msg.MsgID != "99" || msg.Extra["original_msg_id"] != "11" {
		t.Fatalf("msg ids wrong: %+v", msg)
	}
}

func TestConvertWSMessage_OutgoingGroupAndMissingSender(t *testing.T) {
	msg := convertWSMessage(wsMessage{
		MsgID:        12,
		MsgType:      1,
		FromUserName: strField{Str: "wxid_me"},
		ToUserName:   strField{Str: "group@chatroom"},
		Content:      strField{Str: "hi"},
	})
	if msg == nil || !msg.IsGroup || msg.GroupID != "group@chatroom" {
		t.Fatalf("unexpected message: %+v", msg)
	}

	if got := convertWSMessage(wsMessage{}); got != nil {
		t.Fatalf("expected nil for missing from_user_name, got %+v", got)
	}
}

func TestConvertContactEntryAndGroupMember(t *testing.T) {
	contact := convertContactEntry(contactEntry{
		UserName:   strField{Str: "group@chatroom"},
		NickName:   strField{Str: "Group"},
		Remark:     strField{Str: "Remark"},
		Alias:      "alias",
		HeadImgURL: "https://example.com/avatar.jpg",
		Sex:        1,
		Province:   "ON",
		City:       "Toronto",
		Signature:  "sig",
	})
	if contact == nil || !contact.IsGroup || contact.Nickname != "Group" {
		t.Fatalf("unexpected contact: %+v", contact)
	}

	member := convertChatRoomMember(chatRoomMember{
		UserName:    strField{Str: "wxid_user"},
		NickName:    strField{Str: "Alice"},
		DisplayName: "Alice D",
		HeadImgURL:  "https://example.com/a.jpg",
	})
	if member == nil || member.DisplayName != "Alice D" {
		t.Fatalf("unexpected member: %+v", member)
	}
}

func TestConvertSnsAndFinderObjects(t *testing.T) {
	moment := convertSnsObject(snsObject{
		ID:           "m1",
		UserName:     "wxid_user",
		NickName:     "Alice",
		Content:      "hi",
		CreateTime:   123,
		LikeCount:    2,
		CommentCount: 3,
		MediaList:    []snsMedia{{URL: "https://example.com/1.jpg"}, {URL: "https://example.com/2.jpg"}},
		Location:     &snsLocation{Latitude: 1.2, Longitude: 3.4, POIName: "POI"},
	})
	if moment == nil || len(moment.MediaURLs) != 2 || moment.Location == nil {
		t.Fatalf("unexpected moment: %+v", moment)
	}

	video := convertFinderVideo(finderVideo{
		ObjectID:   "v1",
		AuthorID:   "wxid_author",
		AuthorName: "Bob",
		Title:      "Video",
		Desc:       "Desc",
		CoverURL:   "https://example.com/cover.jpg",
		VideoURL:   "https://example.com/video.mp4",
		Duration:   12,
		ShareURL:   "https://example.com/share",
		CreateTime: 456,
	})
	if video == nil || video.VideoID != "v1" || video.AuthorName != "Bob" {
		t.Fatalf("unexpected video: %+v", video)
	}
}
