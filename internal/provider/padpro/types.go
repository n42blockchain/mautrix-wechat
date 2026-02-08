package padpro

// WeChatPadPro API types.
// All endpoints use ?key=<auth_key> query parameter for authentication.
// String fields in WebSocket messages are wrapped in {str: "value"} objects.

// strField is the nested string wrapper used by WeChatPadPro in message payloads.
type strField struct {
	Str string `json:"str"`
}

// wsMessage is the raw WebSocket/Webhook message format from WeChatPadPro.
type wsMessage struct {
	MsgID        int64    `json:"msg_id"`
	FromUserName strField `json:"from_user_name"`
	ToUserName   strField `json:"to_user_name"`
	MsgType      int      `json:"msg_type"`
	Content      strField `json:"content"`
	CreateTime   int64    `json:"create_time"`
	MsgSource    string   `json:"msg_source"`
	PushContent  string   `json:"push_content"`
	NewMsgID     int64    `json:"new_msg_id"`
}

// --- Login API ---

type loginQRCodeResponse struct {
	QRCode string `json:"qr_code"` // base64 encoded QR code image
	QRURL  string `json:"qr_url"`  // QR code URL for scanning
	UUID   string `json:"uuid"`    // session UUID for status polling
}

type loginStatusResponse struct {
	Status   int    `json:"status"`    // 0=waiting, 1=scanned, 2=confirmed, 3=expired
	UserName string `json:"user_name"` // wxid after login
	NickName string `json:"nick_name"`
	HeadURL  string `json:"head_url"`
}

// --- Message send API ---

type sendTextRequest struct {
	ToUserName string `json:"to_user_name"`
	Content    string `json:"content"`
}

type sendImageRequest struct {
	ToUserName string `json:"to_user_name"`
	ImageURL   string `json:"image_url,omitempty"`
	ImageData  string `json:"image_data,omitempty"` // base64 encoded
}

type sendVideoRequest struct {
	ToUserName string `json:"to_user_name"`
	VideoURL   string `json:"video_url,omitempty"`
	ThumbURL   string `json:"thumb_url,omitempty"`
}

type sendVoiceRequest struct {
	ToUserName string `json:"to_user_name"`
	VoiceURL   string `json:"voice_url,omitempty"`
	VoiceData  string `json:"voice_data,omitempty"` // base64 encoded
	Duration   int    `json:"duration"`
}

type sendFileRequest struct {
	ToUserName string `json:"to_user_name"`
	FileURL    string `json:"file_url,omitempty"`
	FileName   string `json:"file_name"`
}

type sendLocationRequest struct {
	ToUserName string  `json:"to_user_name"`
	Latitude   float64 `json:"latitude"`
	Longitude  float64 `json:"longitude"`
	Label      string  `json:"label"`
	Poiname    string  `json:"poiname"`
}

type sendLinkRequest struct {
	ToUserName  string `json:"to_user_name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	ThumbURL    string `json:"thumb_url"`
}

type revokeRequest struct {
	ToUserName string `json:"to_user_name"`
	MsgID      string `json:"msg_id"`
	NewMsgID   string `json:"new_msg_id"`
}

type sendMsgResponse struct {
	MsgID    int64  `json:"msg_id"`
	NewMsgID int64  `json:"new_msg_id"`
	ErrMsg   string `json:"err_msg,omitempty"`
}

// --- Contact API ---

type contactEntry struct {
	UserName   strField `json:"user_name"`
	NickName   strField `json:"nick_name"`
	Remark     strField `json:"remark"`
	Alias      string   `json:"alias"`
	HeadImgURL string   `json:"head_img_url"`
	Sex        int      `json:"sex"`
	Province   string   `json:"province"`
	City       string   `json:"city"`
	Signature  string   `json:"signature"`
}

type contactDetailRequest struct {
	UserNames []string `json:"user_names"`
}

type contactDetailResponse struct {
	Contacts []contactEntry `json:"contacts"`
}

type friendListResponse struct {
	Friends []string `json:"friends"` // list of wxid strings
}

type verifyUserRequest struct {
	EncryptUserName string `json:"encrypt_user_name"`
	Ticket          string `json:"ticket"`
	Scene           int    `json:"scene"`
	Content         string `json:"content"`
}

type setRemarkRequest struct {
	UserName string `json:"user_name"`
	Remark   string `json:"remark"`
}

// --- Group API ---

type chatRoomInfoResponse struct {
	ChatRoomName strField         `json:"chat_room_name"`
	NickName     strField         `json:"nick_name"`
	MemberCount  int              `json:"member_count"`
	Members      []chatRoomMember `json:"members"`
	Announcement string           `json:"announcement"`
	Owner        string           `json:"owner"`
}

type chatRoomMember struct {
	UserName    strField `json:"user_name"`
	NickName    strField `json:"nick_name"`
	DisplayName string   `json:"display_name"`
	HeadImgURL  string   `json:"head_img_url"`
}

type createChatRoomRequest struct {
	UserNames []string `json:"user_names"`
}

type createChatRoomResponse struct {
	ChatRoomName string `json:"chat_room_name"`
}

type chatRoomMembersRequest struct {
	ChatRoomName string   `json:"chat_room_name"`
	UserNames    []string `json:"user_names"`
}

type setChatRoomNameRequest struct {
	ChatRoomName string `json:"chat_room_name"`
	NickName     string `json:"nick_name"`
}

type setChatRoomAnnouncementRequest struct {
	ChatRoomName string `json:"chat_room_name"`
	Announcement string `json:"announcement"`
}

type quitChatRoomRequest struct {
	ChatRoomName string `json:"chat_room_name"`
}

// --- SNS (Moments) API ---

type snsTimelineResponse struct {
	Objects []snsObject `json:"objects"`
}

type snsObject struct {
	ID           string       `json:"id"`
	UserName     string       `json:"user_name"`
	NickName     string       `json:"nick_name"`
	Content      string       `json:"content"`
	MediaList    []snsMedia   `json:"media_list"`
	CreateTime   int64        `json:"create_time"`
	LikeCount    int          `json:"like_count"`
	CommentCount int          `json:"comment_count"`
	Location     *snsLocation `json:"location,omitempty"`
}

type snsMedia struct {
	Type int    `json:"type"` // 1=image, 2=video
	URL  string `json:"url"`
}

type snsLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	POIName   string  `json:"poi_name"`
}

type snsCommentRequest struct {
	ObjectID string `json:"object_id"`
	Content  string `json:"content"`
}

type snsFriendCircleRequest struct {
	Content   string   `json:"content"`
	ImageURLs []string `json:"image_urls,omitempty"`
}

// --- Channels (Finder) API ---

type finderSearchRequest struct {
	Keyword string `json:"keyword"`
}

type finderSearchResponse struct {
	Videos []finderVideo `json:"videos"`
}

type finderVideo struct {
	ObjectID   string `json:"object_id"`
	AuthorID   string `json:"author_id"`
	AuthorName string `json:"author_name"`
	Title      string `json:"title"`
	Desc       string `json:"desc"`
	CoverURL   string `json:"cover_url"`
	VideoURL   string `json:"video_url"`
	Duration   int    `json:"duration"`
	ShareURL   string `json:"share_url"`
	CreateTime int64  `json:"create_time"`
}

// --- Webhook config API ---

type webhookConfigRequest struct {
	URL     string `json:"url"`
	Token   string `json:"token,omitempty"`
	Enabled bool   `json:"enabled"`
}
