package wecom

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// --- WeCom API response types for contacts ---

// departmentListResponse from /cgi-bin/department/list.
type departmentListResponse struct {
	APIResponse
	Department []departmentInfo `json:"department"`
}

type departmentInfo struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	ParentID int    `json:"parentid"`
	Order    int    `json:"order"`
}

// userListResponse from /cgi-bin/user/list.
type userListResponse struct {
	APIResponse
	UserList []userInfo `json:"userlist"`
}

// userInfoResponse from /cgi-bin/user/get.
type userInfoResponse struct {
	APIResponse
	userInfo
}

type userInfo struct {
	UserID     string `json:"userid"`
	Name       string `json:"name"`
	Department []int  `json:"department"`
	Position   string `json:"position"`
	Mobile     string `json:"mobile"`
	Gender     string `json:"gender"`
	Email      string `json:"email"`
	Avatar     string `json:"avatar"`
	ThumbAvatar string `json:"thumb_avatar"`
	Status     int    `json:"status"`
	Alias      string `json:"alias"`
	Address    string `json:"address"`
}

// externalContactListResponse from /cgi-bin/externalcontact/list.
type externalContactListResponse struct {
	APIResponse
	ExternalUserIDs []string `json:"external_userid"`
}

// externalContactInfoResponse from /cgi-bin/externalcontact/get.
type externalContactInfoResponse struct {
	APIResponse
	ExternalContact externalContact `json:"external_contact"`
	FollowUser      []followUser    `json:"follow_user"`
}

type externalContact struct {
	ExternalUserID string `json:"external_userid"`
	Name           string `json:"name"`
	Avatar         string `json:"avatar"`
	Type           int    `json:"type"`   // 1=WeChat 2=Enterprise
	Gender         int    `json:"gender"` // 0=unknown 1=male 2=female
	CorpName       string `json:"corp_name"`
	CorpFullName   string `json:"corp_full_name"`
	Position       string `json:"position"`
}

type followUser struct {
	UserID      string `json:"userid"`
	Remark      string `json:"remark"`
	Description string `json:"description"`
	AddWay      int    `json:"add_way"`
	State       string `json:"state"`
}

// --- Group Chat API response types ---

// appChatCreateRequest for /cgi-bin/appchat/create.
type appChatCreateRequest struct {
	Name     string   `json:"name"`
	Owner    string   `json:"owner,omitempty"`
	UserList []string `json:"userlist"`
	ChatID   string   `json:"chatid,omitempty"`
}

type appChatCreateResponse struct {
	APIResponse
	ChatID string `json:"chatid"`
}

// appChatInfoResponse from /cgi-bin/appchat/get.
type appChatInfoResponse struct {
	APIResponse
	ChatInfo appChatInfo `json:"chat_info"`
}

type appChatInfo struct {
	ChatID   string   `json:"chatid"`
	Name     string   `json:"name"`
	Owner    string   `json:"owner"`
	UserList []string `json:"userlist"`
}

// appChatUpdateRequest for /cgi-bin/appchat/update.
type appChatUpdateRequest struct {
	ChatID      string   `json:"chatid"`
	Name        string   `json:"name,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	AddUserList []string `json:"add_user_list,omitempty"`
	DelUserList []string `json:"del_user_list,omitempty"`
}

// --- Contact management implementation ---

// GetContactList returns all internal users across all departments.
func (p *Provider) GetContactList(ctx context.Context) ([]*wechat.ContactInfo, error) {
	// First, get all departments
	var deptResp departmentListResponse
	if err := p.client.Get(ctx, "/cgi-bin/department/list", &deptResp); err != nil {
		return nil, fmt.Errorf("list departments: %w", err)
	}
	if deptResp.ErrCode != 0 {
		return nil, fmt.Errorf("list departments: [%d] %s", deptResp.ErrCode, deptResp.ErrMsg)
	}

	var allContacts []*wechat.ContactInfo
	seen := make(map[string]bool)

	// Get users from each department
	for _, dept := range deptResp.Department {
		var userResp userListResponse
		path := fmt.Sprintf("/cgi-bin/user/list?department_id=%d", dept.ID)
		if err := p.client.Get(ctx, path, &userResp); err != nil {
			p.log.Warn("list users failed for department",
				"dept_id", dept.ID, "error", err)
			continue
		}
		if userResp.ErrCode != 0 {
			continue
		}

		for _, u := range userResp.UserList {
			if seen[u.UserID] {
				continue
			}
			seen[u.UserID] = true

			allContacts = append(allContacts, wecomUserToContact(&u))
		}
	}

	return allContacts, nil
}

// GetContactInfo returns info for a specific user.
func (p *Provider) GetContactInfo(ctx context.Context, userID string) (*wechat.ContactInfo, error) {
	// Try internal user first
	var userResp userInfoResponse
	path := fmt.Sprintf("/cgi-bin/user/get?userid=%s", userID)
	if err := p.client.Get(ctx, path, &userResp); err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	if userResp.ErrCode == 0 {
		return wecomUserToContact(&userResp.userInfo), nil
	}

	// Try external contact
	var extResp externalContactInfoResponse
	extPath := fmt.Sprintf("/cgi-bin/externalcontact/get?external_userid=%s", userID)
	if err := p.client.Get(ctx, extPath, &extResp); err != nil {
		return nil, fmt.Errorf("get external contact: %w", err)
	}

	if extResp.ErrCode != 0 {
		return nil, fmt.Errorf("contact not found: %s", userID)
	}

	return &wechat.ContactInfo{
		UserID:    extResp.ExternalContact.ExternalUserID,
		Nickname:  extResp.ExternalContact.Name,
		AvatarURL: extResp.ExternalContact.Avatar,
		Gender:    extResp.ExternalContact.Gender,
	}, nil
}

// GetUserAvatar downloads a user's avatar.
func (p *Provider) GetUserAvatar(ctx context.Context, userID string) ([]byte, string, error) {
	contact, err := p.GetContactInfo(ctx, userID)
	if err != nil {
		return nil, "", err
	}

	if contact.AvatarURL == "" {
		return nil, "", fmt.Errorf("user %s has no avatar", userID)
	}

	resp, err := p.client.httpClient.Get(contact.AvatarURL)
	if err != nil {
		return nil, "", fmt.Errorf("download avatar: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read avatar: %w", err)
	}

	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "image/jpeg"
	}

	return data, mimeType, nil
}

// AcceptFriendRequest is not applicable for WeCom (enterprise contacts).
func (p *Provider) AcceptFriendRequest(ctx context.Context, xmlData string) error {
	return fmt.Errorf("wecom: friend requests not applicable in enterprise context")
}

// SetContactRemark sets the remark for an external contact.
func (p *Provider) SetContactRemark(ctx context.Context, userID string, remark string) error {
	var resp APIResponse
	err := p.client.PostJSON(ctx, "/cgi-bin/externalcontact/remark", map[string]interface{}{
		"userid":          p.getSelfID(),
		"external_userid": userID,
		"remark":          remark,
	}, &resp)
	if err != nil {
		return err
	}
	if resp.ErrCode != 0 {
		return fmt.Errorf("set remark: [%d] %s", resp.ErrCode, resp.ErrMsg)
	}
	return nil
}

// --- Group management ---

// GetGroupList returns all group chats created by the app.
// Note: WeCom only provides access to group chats created through the app API.
func (p *Provider) GetGroupList(ctx context.Context) ([]*wechat.ContactInfo, error) {
	// WeCom doesn't have a "list all groups" API.
	// Group chats are tracked by the bridge's room_mapping table.
	// Return empty for now — groups are discovered via callbacks and appchat/get.
	p.log.Info("GetGroupList: WeCom does not support listing all groups")
	return []*wechat.ContactInfo{}, nil
}

// GetGroupMembers returns members of a group chat.
func (p *Provider) GetGroupMembers(ctx context.Context, groupID string) ([]*wechat.GroupMember, error) {
	info, err := p.getAppChat(ctx, groupID)
	if err != nil {
		return nil, err
	}

	var members []*wechat.GroupMember
	for _, uid := range info.UserList {
		member := &wechat.GroupMember{
			UserID:   uid,
			Nickname: uid, // Will be resolved via contact info
			IsOwner:  uid == info.Owner,
		}
		members = append(members, member)
	}

	return members, nil
}

// GetGroupInfo returns info for a specific group chat.
func (p *Provider) GetGroupInfo(ctx context.Context, groupID string) (*wechat.ContactInfo, error) {
	info, err := p.getAppChat(ctx, groupID)
	if err != nil {
		return nil, err
	}

	return &wechat.ContactInfo{
		UserID:      info.ChatID,
		Nickname:    info.Name,
		IsGroup:     true,
		MemberCount: len(info.UserList),
	}, nil
}

// CreateGroup creates a new group chat.
func (p *Provider) CreateGroup(ctx context.Context, name string, members []string) (string, error) {
	var resp appChatCreateResponse
	err := p.client.PostJSON(ctx, "/cgi-bin/appchat/create", &appChatCreateRequest{
		Name:     name,
		Owner:    p.getSelfID(),
		UserList: members,
	}, &resp)
	if err != nil {
		return "", err
	}
	if resp.ErrCode != 0 {
		return "", fmt.Errorf("create group: [%d] %s", resp.ErrCode, resp.ErrMsg)
	}
	return resp.ChatID, nil
}

// InviteToGroup adds users to a group chat.
func (p *Provider) InviteToGroup(ctx context.Context, groupID string, userIDs []string) error {
	var resp APIResponse
	err := p.client.PostJSON(ctx, "/cgi-bin/appchat/update", &appChatUpdateRequest{
		ChatID:      groupID,
		AddUserList: userIDs,
	}, &resp)
	if err != nil {
		return err
	}
	if resp.ErrCode != 0 {
		return fmt.Errorf("invite to group: [%d] %s", resp.ErrCode, resp.ErrMsg)
	}
	return nil
}

// RemoveFromGroup removes users from a group chat.
func (p *Provider) RemoveFromGroup(ctx context.Context, groupID string, userIDs []string) error {
	var resp APIResponse
	err := p.client.PostJSON(ctx, "/cgi-bin/appchat/update", &appChatUpdateRequest{
		ChatID:      groupID,
		DelUserList: userIDs,
	}, &resp)
	if err != nil {
		return err
	}
	if resp.ErrCode != 0 {
		return fmt.Errorf("remove from group: [%d] %s", resp.ErrCode, resp.ErrMsg)
	}
	return nil
}

// SetGroupName changes the name of a group chat.
func (p *Provider) SetGroupName(ctx context.Context, groupID string, name string) error {
	var resp APIResponse
	err := p.client.PostJSON(ctx, "/cgi-bin/appchat/update", &appChatUpdateRequest{
		ChatID: groupID,
		Name:   name,
	}, &resp)
	if err != nil {
		return err
	}
	if resp.ErrCode != 0 {
		return fmt.Errorf("set group name: [%d] %s", resp.ErrCode, resp.ErrMsg)
	}
	return nil
}

// SetGroupAnnouncement is not directly supported by WeCom app chat API.
func (p *Provider) SetGroupAnnouncement(ctx context.Context, groupID string, text string) error {
	// WeCom app chat API does not support announcements.
	// Send it as a text message instead.
	_, err := p.SendText(ctx, groupID, fmt.Sprintf("[Announcement] %s", text))
	return err
}

// LeaveGroup is not directly supported by WeCom app chat API.
func (p *Provider) LeaveGroup(ctx context.Context, groupID string) error {
	return fmt.Errorf("wecom: leaving groups not supported via API")
}

// --- Helpers ---

// getAppChat fetches app chat info.
func (p *Provider) getAppChat(ctx context.Context, chatID string) (*appChatInfo, error) {
	var resp appChatInfoResponse
	path := fmt.Sprintf("/cgi-bin/appchat/get?chatid=%s", chatID)
	if err := p.client.Get(ctx, path, &resp); err != nil {
		return nil, fmt.Errorf("get group: %w", err)
	}
	if resp.ErrCode != 0 {
		return nil, fmt.Errorf("get group: [%d] %s", resp.ErrCode, resp.ErrMsg)
	}
	return &resp.ChatInfo, nil
}

// getSelfID returns the operator's user ID (used as owner for group operations).
func (p *Provider) getSelfID() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.self != nil {
		return p.self.UserID
	}
	return ""
}

// wecomUserToContact converts a WeCom user to a generic ContactInfo.
func wecomUserToContact(u *userInfo) *wechat.ContactInfo {
	gender := 0
	if g, err := strconv.Atoi(u.Gender); err == nil {
		gender = g
	}

	return &wechat.ContactInfo{
		UserID:    u.UserID,
		Alias:     u.Alias,
		Nickname:  u.Name,
		AvatarURL: u.Avatar,
		Gender:    gender,
		Signature: u.Position,
	}
}
