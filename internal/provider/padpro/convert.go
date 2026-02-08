package padpro

import (
	"strconv"
	"strings"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// convertWSMessage transforms a raw WeChatPadPro WebSocket/Webhook message
// into the unified wechat.Message format.
//
// Group message detection: WeChatPadPro sends group messages with the group ID
// (ending in @chatroom) as from_user_name. The real sender's wxid is embedded
// in the content before a ":\n" separator.
//
// Returns nil if the message cannot be converted (e.g. missing from_user_name).
func convertWSMessage(raw wsMessage) *wechat.Message {
	fromUser := raw.FromUserName.Str
	toUser := raw.ToUserName.Str
	if fromUser == "" {
		return nil
	}

	// Prefer NewMsgID (64-bit) over MsgID for deduplication
	msgID := strconv.FormatInt(raw.NewMsgID, 10)
	if raw.NewMsgID == 0 {
		msgID = strconv.FormatInt(raw.MsgID, 10)
	}

	msg := &wechat.Message{
		MsgID:     msgID,
		Type:      wechat.MsgType(raw.MsgType),
		FromUser:  fromUser,
		ToUser:    toUser,
		Content:   raw.Content.Str,
		Timestamp: raw.CreateTime * 1000, // seconds → milliseconds
		Extra:     make(map[string]string),
	}

	// Detect group messages: group IDs end with @chatroom
	if strings.HasSuffix(fromUser, "@chatroom") {
		msg.IsGroup = true
		msg.GroupID = fromUser
		// In group messages, the actual sender wxid is prefixed in content: "wxid_xxx:\n<real content>"
		if idx := strings.Index(msg.Content, ":\n"); idx > 0 {
			msg.FromUser = msg.Content[:idx]
			msg.Content = msg.Content[idx+2:]
		}
	} else if strings.HasSuffix(toUser, "@chatroom") {
		// Outgoing group message (sent by self)
		msg.IsGroup = true
		msg.GroupID = toUser
	}

	// Preserve raw fields for debugging and advanced processing
	if raw.MsgSource != "" {
		msg.Extra["msg_source"] = raw.MsgSource
	}
	if raw.PushContent != "" {
		msg.Extra["push_content"] = raw.PushContent
	}
	if raw.MsgID != 0 {
		msg.Extra["original_msg_id"] = strconv.FormatInt(raw.MsgID, 10)
	}

	return msg
}

// convertContactEntry transforms a WeChatPadPro contact entry to wechat.ContactInfo.
func convertContactEntry(entry contactEntry) *wechat.ContactInfo {
	return &wechat.ContactInfo{
		UserID:    entry.UserName.Str,
		Alias:     entry.Alias,
		Nickname:  entry.NickName.Str,
		Remark:    entry.Remark.Str,
		AvatarURL: entry.HeadImgURL,
		Gender:    entry.Sex,
		Province:  entry.Province,
		City:      entry.City,
		Signature: entry.Signature,
		IsGroup:   strings.HasSuffix(entry.UserName.Str, "@chatroom"),
	}
}

// convertChatRoomMember transforms a WeChatPadPro chat room member to wechat.GroupMember.
func convertChatRoomMember(m chatRoomMember) *wechat.GroupMember {
	return &wechat.GroupMember{
		UserID:      m.UserName.Str,
		Nickname:    m.NickName.Str,
		DisplayName: m.DisplayName,
		AvatarURL:   m.HeadImgURL,
	}
}

// convertSnsObject transforms a WeChatPadPro SNS object to wechat.MomentEntry.
func convertSnsObject(obj snsObject) *wechat.MomentEntry {
	entry := &wechat.MomentEntry{
		MomentID:     obj.ID,
		UserID:       obj.UserName,
		Nickname:     obj.NickName,
		Content:      obj.Content,
		LikeCount:    obj.LikeCount,
		CommentCount: obj.CommentCount,
		Timestamp:    obj.CreateTime * 1000,
		Extra:        make(map[string]string),
	}

	for _, media := range obj.MediaList {
		entry.MediaURLs = append(entry.MediaURLs, media.URL)
	}

	if obj.Location != nil {
		entry.Location = &wechat.LocationInfo{
			Latitude:  obj.Location.Latitude,
			Longitude: obj.Location.Longitude,
			Poiname:   obj.Location.POIName,
		}
	}

	return entry
}

// convertFinderVideo transforms a WeChatPadPro finder video to wechat.ChannelsVideo.
func convertFinderVideo(v finderVideo) *wechat.ChannelsVideo {
	return &wechat.ChannelsVideo{
		VideoID:     v.ObjectID,
		AuthorID:    v.AuthorID,
		AuthorName:  v.AuthorName,
		Title:       v.Title,
		Description: v.Desc,
		CoverURL:    v.CoverURL,
		VideoURL:    v.VideoURL,
		Duration:    v.Duration,
		ShareURL:    v.ShareURL,
		Timestamp:   v.CreateTime * 1000,
	}
}
