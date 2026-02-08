package pchook

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// rawMessage represents a WeChatFerry raw message notification.
type rawMessage struct {
	MsgID     string `json:"msg_id"`
	Type      int    `json:"type"`
	Sender    string `json:"sender"`
	RoomID    string `json:"room_id"` // empty for DM, group ID for group
	Content   string `json:"content"`
	Thumb     string `json:"thumb"`
	Extra     string `json:"extra"`
	MediaPath string `json:"media_path"`
	Timestamp int64  `json:"timestamp"`
	XML       string `json:"xml"` // raw XML for complex types
}

// parseRawMessage converts a WeChatFerry notification into a wechat.Message.
func parseRawMessage(data json.RawMessage, log *slog.Logger) (*wechat.Message, error) {
	var raw rawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal raw message: %w", err)
	}

	msg := &wechat.Message{
		MsgID:     raw.MsgID,
		Type:      wechat.MsgType(raw.Type),
		Timestamp: raw.Timestamp,
		Content:   raw.Content,
		Extra:     make(map[string]string),
	}

	// Determine sender and group membership
	if raw.RoomID != "" {
		msg.IsGroup = true
		msg.GroupID = raw.RoomID
		msg.FromUser = raw.Sender
		msg.ToUser = raw.RoomID
	} else {
		msg.FromUser = raw.Sender
	}

	// Handle group message: sender may be in "wxid_xxx:\ncontent" format
	if msg.IsGroup && strings.Contains(raw.Content, ":\n") {
		parts := strings.SplitN(raw.Content, ":\n", 2)
		if len(parts) == 2 {
			msg.FromUser = parts[0]
			msg.Content = parts[1]
		}
	}

	// Set media path if available
	if raw.MediaPath != "" {
		msg.Extra["media_path"] = raw.MediaPath
	}
	if raw.Thumb != "" {
		msg.Extra["thumb_path"] = raw.Thumb
	}
	if raw.XML != "" {
		msg.Extra["xml"] = raw.XML
	}

	// Parse type-specific data
	switch msg.Type {
	case wechat.MsgLink:
		parseXMLLink(raw.XML, msg)
	case wechat.MsgLocation:
		parseXMLLocation(raw.XML, msg)
	}

	return msg, nil
}

// parseXMLLink extracts link card info from WeChatFerry XML content.
func parseXMLLink(xml string, msg *wechat.Message) {
	if xml == "" {
		return
	}
	msg.LinkInfo = &wechat.LinkCardInfo{
		Title:       extractXMLField(xml, "title"),
		Description: extractXMLField(xml, "des"),
		URL:         extractXMLField(xml, "url"),
		ThumbURL:    extractXMLField(xml, "thumburl"),
	}
}

// parseXMLLocation extracts location from WeChatFerry XML content.
func parseXMLLocation(xml string, msg *wechat.Message) {
	if xml == "" {
		return
	}
	msg.Location = &wechat.LocationInfo{
		Label:   extractXMLField(xml, "label"),
		Poiname: extractXMLField(xml, "poiname"),
	}
	// Latitude/longitude would need float parsing from XML attrs
}

// extractXMLField performs simple XML field extraction for common patterns.
// This is intentionally simple — full XML parsing isn't needed for the limited
// set of fields WeChatFerry provides.
func extractXMLField(xml, field string) string {
	openTag := "<" + field + ">"
	closeTag := "</" + field + ">"

	start := strings.Index(xml, openTag)
	if start < 0 {
		// Try CDATA variant: <field><![CDATA[...]]></field>
		return extractXMLCData(xml, field)
	}

	start += len(openTag)
	end := strings.Index(xml[start:], closeTag)
	if end < 0 {
		return ""
	}

	value := xml[start : start+end]
	// Strip CDATA wrapper if present
	if strings.HasPrefix(value, "<![CDATA[") && strings.HasSuffix(value, "]]>") {
		value = value[9 : len(value)-3]
	}
	return value
}

// extractXMLCData extracts a CDATA-wrapped field value.
func extractXMLCData(xml, field string) string {
	openTag := "<" + field + "><![CDATA["
	closeTag := "]]></" + field + ">"

	start := strings.Index(xml, openTag)
	if start < 0 {
		return ""
	}
	start += len(openTag)
	end := strings.Index(xml[start:], closeTag)
	if end < 0 {
		return ""
	}
	return xml[start : start+end]
}

// sendTextParams holds parameters for sending a text message via RPC.
type sendTextParams struct {
	ToUser  string `json:"to_user"`
	Content string `json:"content"`
	AtList  string `json:"at_list,omitempty"` // comma-separated wxid list
}

// sendImageParams holds parameters for sending an image via RPC.
type sendImageParams struct {
	ToUser string `json:"to_user"`
	Path   string `json:"path"` // local file path on the Windows host
}

// sendFileParams holds parameters for sending a file via RPC.
type sendFileParams struct {
	ToUser string `json:"to_user"`
	Path   string `json:"path"`
}

// revokeParams holds parameters for revoking a message.
type revokeParams struct {
	MsgID  string `json:"msg_id"`
	ToUser string `json:"to_user"`
}

// contactResult represents a contact entry from WeChatFerry.
type contactResult struct {
	UserID    string `json:"wxid"`
	Alias     string `json:"alias"`
	Nickname  string `json:"nickname"`
	Remark    string `json:"remark"`
	AvatarURL string `json:"avatar"`
	Gender    int    `json:"gender"`
	Province  string `json:"province"`
	City      string `json:"city"`
	Signature string `json:"signature"`
}

func (c *contactResult) toContactInfo() *wechat.ContactInfo {
	return &wechat.ContactInfo{
		UserID:    c.UserID,
		Alias:     c.Alias,
		Nickname:  c.Nickname,
		Remark:    c.Remark,
		AvatarURL: c.AvatarURL,
		Gender:    c.Gender,
		Province:  c.Province,
		City:      c.City,
		Signature: c.Signature,
		IsGroup:   strings.HasSuffix(c.UserID, "@chatroom"),
	}
}

// groupMemberResult represents a group member from WeChatFerry.
type groupMemberResult struct {
	UserID      string `json:"wxid"`
	Nickname    string `json:"nickname"`
	DisplayName string `json:"display_name"`
	IsAdmin     bool   `json:"is_admin"`
}

func (m *groupMemberResult) toGroupMember() *wechat.GroupMember {
	return &wechat.GroupMember{
		UserID:      m.UserID,
		Nickname:    m.Nickname,
		DisplayName: m.DisplayName,
		IsAdmin:     m.IsAdmin,
	}
}
