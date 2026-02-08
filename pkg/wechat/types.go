package wechat

import "io"

// MsgType represents WeChat message types.
type MsgType int

const (
	MsgText     MsgType = 1
	MsgImage    MsgType = 3
	MsgVoice    MsgType = 34
	MsgContact  MsgType = 42
	MsgVideo    MsgType = 43
	MsgEmoji    MsgType = 47
	MsgLocation MsgType = 48
	MsgLink     MsgType = 49
	MsgFile     MsgType = 4903
	MsgMiniApp  MsgType = 4933
	MsgSystem   MsgType = 10000
	MsgRevoke   MsgType = 10002
)

// String returns the string representation of a MsgType.
func (t MsgType) String() string {
	switch t {
	case MsgText:
		return "text"
	case MsgImage:
		return "image"
	case MsgVoice:
		return "voice"
	case MsgContact:
		return "contact"
	case MsgVideo:
		return "video"
	case MsgEmoji:
		return "emoji"
	case MsgLocation:
		return "location"
	case MsgLink:
		return "link"
	case MsgFile:
		return "file"
	case MsgMiniApp:
		return "miniapp"
	case MsgSystem:
		return "system"
	case MsgRevoke:
		return "revoke"
	default:
		return "unknown"
	}
}

// LoginState represents the current login state of a provider.
type LoginState int

const (
	LoginStateLoggedOut  LoginState = iota
	LoginStateQRCode               // Waiting for QR code scan
	LoginStateConfirming            // QR scanned, waiting for confirmation
	LoginStateLoggedIn              // Successfully logged in
	LoginStateError                 // Login error
)

// String returns the string representation of a LoginState.
func (s LoginState) String() string {
	switch s {
	case LoginStateLoggedOut:
		return "logged_out"
	case LoginStateQRCode:
		return "qr_code"
	case LoginStateConfirming:
		return "confirming"
	case LoginStateLoggedIn:
		return "logged_in"
	case LoginStateError:
		return "error"
	default:
		return "unknown"
	}
}

// LoginEvent is emitted during the login process to notify the bridge of state changes.
type LoginEvent struct {
	State  LoginState
	QRCode []byte // QR code image data (PNG)
	QRURL  string // QR code URL
	Error  string // Error message (when State == LoginStateError)
	UserID string // WeChat user ID (when State == LoginStateLoggedIn)
	Name   string // WeChat nickname
	Avatar string // Avatar URL
}

// Message represents a unified WeChat message structure.
type Message struct {
	MsgID     string            // WeChat message ID
	Type      MsgType           // Message type
	FromUser  string            // Sender WeChat ID
	ToUser    string            // Receiver WeChat ID or group ID
	Content   string            // Text content
	MediaURL  string            // Media URL
	MediaData []byte            // Media binary data
	FileName  string            // File name
	FileSize  int64             // File size in bytes
	Duration  int               // Voice/video duration in seconds
	Thumbnail []byte            // Thumbnail data
	Location  *LocationInfo     // Location info
	LinkInfo  *LinkCardInfo     // Link card info
	ReplyTo   string            // Reply-to message ID
	Timestamp int64             // Timestamp in milliseconds
	IsGroup   bool              // Whether this is a group message
	GroupID   string            // Group ID if group message
	Extra     map[string]string // Extension fields
}

// LocationInfo contains geographic location information.
type LocationInfo struct {
	Latitude  float64
	Longitude float64
	Label     string
	Poiname   string
}

// LinkCardInfo contains link card (article/share) information.
type LinkCardInfo struct {
	Title       string
	Description string
	URL         string
	ThumbURL    string
}

// ContactInfo represents a WeChat contact or group.
type ContactInfo struct {
	UserID      string // WeChat ID (wxid_xxx)
	Alias       string // WeChat alias
	Nickname    string // Display name
	Remark      string // Remark name set by the user
	AvatarURL   string // Avatar URL
	Gender      int    // 0: unknown, 1: male, 2: female
	Province    string
	City        string
	Signature   string // Personal signature
	IsGroup     bool   // Whether this is a group
	MemberCount int    // Number of group members
}

// GroupMember represents a member of a WeChat group.
type GroupMember struct {
	UserID      string
	Nickname    string
	DisplayName string // In-group nickname
	AvatarURL   string
	IsAdmin     bool
	IsOwner     bool
	InviterID   string
	JoinTime    int64
}

// Capability describes the feature set supported by a provider.
type Capability struct {
	SendText       bool
	SendImage      bool
	SendVideo      bool
	SendVoice      bool
	SendFile       bool
	SendLocation   bool
	SendLink       bool
	SendMiniApp    bool
	ReceiveMessage bool
	GroupManage     bool
	ContactManage  bool
	MomentAccess   bool
	VoiceCall      bool
	VideoCall      bool
	Revoke         bool
	Reaction       bool
	ReadReceipt    bool
	Typing         bool
}

// MediaDownload represents a downloaded media file.
type MediaDownload struct {
	Reader   io.ReadCloser
	MimeType string
	Size     int64
	FileName string
}
