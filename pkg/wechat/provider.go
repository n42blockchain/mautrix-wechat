package wechat

import (
	"context"
	"io"
)

// MessageHandler is the callback interface that the bridge core implements
// to receive events from a WeChat provider.
type MessageHandler interface {
	OnMessage(ctx context.Context, msg *Message) error
	OnLoginEvent(ctx context.Context, evt *LoginEvent) error
	OnContactUpdate(ctx context.Context, contact *ContactInfo) error
	OnGroupMemberUpdate(ctx context.Context, groupID string, members []*GroupMember) error
	OnPresence(ctx context.Context, userID string, online bool) error
	OnTyping(ctx context.Context, userID string, chatID string) error
	OnRevoke(ctx context.Context, msgID string, replaceTip string) error
}

// Provider is the core interface that all WeChat access methods must implement.
// Each tier (WeCom, iPad Protocol, PC Hook, etc.) provides a concrete implementation.
type Provider interface {
	// Lifecycle

	// Init initializes the provider with configuration and the message handler callback.
	Init(cfg *ProviderConfig, handler MessageHandler) error
	// Start begins the provider's event loop.
	Start(ctx context.Context) error
	// Stop gracefully shuts down the provider.
	Stop() error
	// IsRunning returns whether the provider is currently active.
	IsRunning() bool

	// Identity

	// Name returns the provider name, e.g. "wecom", "ipad", "pchook".
	Name() string
	// Tier returns the provider tier (1-5).
	Tier() int
	// Capabilities returns the feature set supported by this provider.
	Capabilities() Capability

	// Authentication

	// Login triggers the login flow. QR code and status updates are delivered
	// via the MessageHandler.OnLoginEvent callback.
	Login(ctx context.Context) error
	// Logout logs out the current session.
	Logout(ctx context.Context) error
	// GetLoginState returns the current login state.
	GetLoginState() LoginState
	// GetSelf returns the currently logged-in account info.
	GetSelf() *ContactInfo

	// Messaging

	// SendText sends a text message and returns the message ID.
	SendText(ctx context.Context, toUser string, text string) (string, error)
	// SendImage sends an image and returns the message ID.
	SendImage(ctx context.Context, toUser string, data io.Reader, filename string) (string, error)
	// SendVideo sends a video with an optional thumbnail.
	SendVideo(ctx context.Context, toUser string, data io.Reader, filename string, thumb io.Reader) (string, error)
	// SendVoice sends a voice message with the given duration in seconds.
	SendVoice(ctx context.Context, toUser string, data io.Reader, duration int) (string, error)
	// SendFile sends a file attachment.
	SendFile(ctx context.Context, toUser string, data io.Reader, filename string) (string, error)
	// SendLocation sends a location message.
	SendLocation(ctx context.Context, toUser string, loc *LocationInfo) (string, error)
	// SendLink sends a link card message.
	SendLink(ctx context.Context, toUser string, link *LinkCardInfo) (string, error)
	// RevokeMessage revokes (recalls) a previously sent message.
	RevokeMessage(ctx context.Context, msgID string, toUser string) error

	// Contacts

	// GetContactList returns all contacts.
	GetContactList(ctx context.Context) ([]*ContactInfo, error)
	// GetContactInfo returns info for a specific contact.
	GetContactInfo(ctx context.Context, userID string) (*ContactInfo, error)
	// GetUserAvatar downloads a user's avatar, returning the data and MIME type.
	GetUserAvatar(ctx context.Context, userID string) ([]byte, string, error)
	// AcceptFriendRequest accepts a friend request given the raw XML payload.
	AcceptFriendRequest(ctx context.Context, xml string) error
	// SetContactRemark sets the remark name for a contact.
	SetContactRemark(ctx context.Context, userID string, remark string) error

	// Groups

	// GetGroupList returns all groups the account belongs to.
	GetGroupList(ctx context.Context) ([]*ContactInfo, error)
	// GetGroupMembers returns members of a specific group.
	GetGroupMembers(ctx context.Context, groupID string) ([]*GroupMember, error)
	// GetGroupInfo returns info for a specific group.
	GetGroupInfo(ctx context.Context, groupID string) (*ContactInfo, error)
	// CreateGroup creates a new group with the given name and initial members.
	CreateGroup(ctx context.Context, name string, members []string) (string, error)
	// InviteToGroup invites users to a group.
	InviteToGroup(ctx context.Context, groupID string, userIDs []string) error
	// RemoveFromGroup removes users from a group.
	RemoveFromGroup(ctx context.Context, groupID string, userIDs []string) error
	// SetGroupName changes the group name.
	SetGroupName(ctx context.Context, groupID string, name string) error
	// SetGroupAnnouncement sets the group announcement text.
	SetGroupAnnouncement(ctx context.Context, groupID string, text string) error
	// LeaveGroup leaves a group.
	LeaveGroup(ctx context.Context, groupID string) error

	// Media

	// DownloadMedia downloads media from a message, returning a reader and MIME type.
	DownloadMedia(ctx context.Context, msg *Message) (io.ReadCloser, string, error)
}

// ProviderConfig holds configuration for a provider instance.
type ProviderConfig struct {
	// Common
	DataDir  string
	LogLevel string

	// WeCom (Tier 1)
	CorpID    string
	AppSecret string
	AgentID   int
	Token     string
	AESKey    string

	// iPad Protocol (Tier 2)
	APIEndpoint string
	APIToken    string
	CallbackURL string

	// PC Hook (Tier 3)
	WeChatPath string
	DLLPath    string
	RPCPort    int

	// Extension fields
	Extra map[string]string
}
