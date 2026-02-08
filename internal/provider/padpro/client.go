package padpro

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client wraps the WeChatPadPro REST API.
// All endpoints are authenticated via ?key=<authKey> query parameter.
//
// API reference:
//   - Login:    /login/GetLoginQrCodeNew, /login/CheckLoginStatus, /login/LogOut
//   - Message:  /message/SendTextMessage, /message/SendImageMessage, /message/SendVoice,
//               /message/CdnUploadVideo, /message/RevokeMsg, /message/sendFile
//   - Contact:  /friend/GetFriendList, /friend/GetContactDetailsList, /friend/AgreeAdd
//   - Group:    /group/CreateChatRoom, /group/AddChatRoomMembers, /group/GetChatRoomInfo
//   - SNS:      /sns/GetSnsSync, /sns/SendFriendCircle, /sns/SendSnsComment
//   - Finder:   /finder/FinderSearch, /finder/FinderFollow
//   - Webhook:  /v1/webhook/Config
type Client struct {
	baseURL string
	authKey string
	httpCli *http.Client
}

// apiResponse is the standard envelope from WeChatPadPro REST API.
type apiResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data,omitempty"`
}

// NewClient creates a new WeChatPadPro API client.
func NewClient(baseURL, authKey string) *Client {
	return &Client{
		baseURL: baseURL,
		authKey: authKey,
		httpCli: &http.Client{Timeout: 30 * time.Second},
	}
}

// buildURL constructs the full URL with ?key= auth parameter.
func (c *Client) buildURL(path string) string {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return c.baseURL + path + "?key=" + c.authKey
	}
	q := u.Query()
	q.Set("key", c.authKey)
	u.RawQuery = q.Encode()
	return u.String()
}

// Get performs an authenticated GET request.
func (c *Client) Get(ctx context.Context, path string) (*apiResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.buildURL(path), nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

// PostJSON performs an authenticated POST request with a JSON body.
func (c *Client) PostJSON(ctx context.Context, path string, body interface{}) (*apiResponse, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.buildURL(path), reqBody)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req)
}

// do executes an HTTP request, reads the response, and validates the API status code.
func (c *Client) do(req *http.Request) (*apiResponse, error) {
	resp, err := c.httpCli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}

	var apiResp apiResponse
	if err := json.Unmarshal(data, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if apiResp.Code != 0 && apiResp.Code != 200 {
		return nil, fmt.Errorf("API error [%d]: %s", apiResp.Code, apiResp.Msg)
	}

	return &apiResp, nil
}

// ParseData unmarshals the Data field from an API response into the target.
func (c *Client) ParseData(resp *apiResponse, target interface{}) error {
	if resp.Data == nil {
		return fmt.Errorf("response has no data field")
	}
	return json.Unmarshal(resp.Data, target)
}

// --- Login API ---

// GetLoginQRCode requests a new QR code for login.
func (c *Client) GetLoginQRCode(ctx context.Context) (*loginQRCodeResponse, error) {
	resp, err := c.PostJSON(ctx, "/login/GetLoginQrCodeNew", nil)
	if err != nil {
		return nil, fmt.Errorf("get login QR code: %w", err)
	}
	var data loginQRCodeResponse
	if err := c.ParseData(resp, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// CheckLoginStatus polls the login status after QR code is shown.
func (c *Client) CheckLoginStatus(ctx context.Context) (*loginStatusResponse, error) {
	resp, err := c.Get(ctx, "/login/CheckLoginStatus")
	if err != nil {
		return nil, fmt.Errorf("check login status: %w", err)
	}
	var data loginStatusResponse
	if err := c.ParseData(resp, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// Logout terminates the current session.
func (c *Client) Logout(ctx context.Context) error {
	_, err := c.Get(ctx, "/login/LogOut")
	return err
}

// --- Message API ---

// SendTextMessage sends a text message.
func (c *Client) SendTextMessage(ctx context.Context, req *sendTextRequest) (*sendMsgResponse, error) {
	resp, err := c.PostJSON(ctx, "/message/SendTextMessage", req)
	if err != nil {
		return nil, err
	}
	var data sendMsgResponse
	if err := c.ParseData(resp, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// SendImageMessage sends an image. ImageData should be base64 encoded.
func (c *Client) SendImageMessage(ctx context.Context, req *sendImageRequest) (*sendMsgResponse, error) {
	resp, err := c.PostJSON(ctx, "/message/SendImageMessage", req)
	if err != nil {
		return nil, err
	}
	var data sendMsgResponse
	if err := c.ParseData(resp, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// SendVoice sends a voice message.
func (c *Client) SendVoice(ctx context.Context, req *sendVoiceRequest) (*sendMsgResponse, error) {
	resp, err := c.PostJSON(ctx, "/message/SendVoice", req)
	if err != nil {
		return nil, err
	}
	var data sendMsgResponse
	if err := c.ParseData(resp, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// CdnUploadVideo uploads and sends a video.
func (c *Client) CdnUploadVideo(ctx context.Context, req *sendVideoRequest) (*sendMsgResponse, error) {
	resp, err := c.PostJSON(ctx, "/message/CdnUploadVideo", req)
	if err != nil {
		return nil, err
	}
	var data sendMsgResponse
	if err := c.ParseData(resp, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// SendFile sends a file.
func (c *Client) SendFile(ctx context.Context, req *sendFileRequest) (*sendMsgResponse, error) {
	resp, err := c.PostJSON(ctx, "/message/sendFile", req)
	if err != nil {
		return nil, err
	}
	var data sendMsgResponse
	if err := c.ParseData(resp, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// RevokeMsg revokes a sent message.
func (c *Client) RevokeMsg(ctx context.Context, req *revokeRequest) error {
	_, err := c.PostJSON(ctx, "/message/RevokeMsg", req)
	return err
}

// --- Contact API ---

// GetFriendList returns the list of friend wxid strings.
func (c *Client) GetFriendList(ctx context.Context) ([]string, error) {
	resp, err := c.PostJSON(ctx, "/friend/GetFriendList", nil)
	if err != nil {
		return nil, err
	}
	var data friendListResponse
	if err := c.ParseData(resp, &data); err != nil {
		return nil, err
	}
	return data.Friends, nil
}

// GetContactDetailsList fetches detailed info for a batch of user IDs.
func (c *Client) GetContactDetailsList(ctx context.Context, userNames []string) ([]contactEntry, error) {
	resp, err := c.PostJSON(ctx, "/friend/GetContactDetailsList", &contactDetailRequest{
		UserNames: userNames,
	})
	if err != nil {
		return nil, err
	}
	var data contactDetailResponse
	if err := c.ParseData(resp, &data); err != nil {
		return nil, err
	}
	return data.Contacts, nil
}

// AgreeAdd accepts a friend request.
func (c *Client) AgreeAdd(ctx context.Context, encryptUserName, ticket string, scene int) error {
	_, err := c.PostJSON(ctx, "/friend/AgreeAdd", &verifyUserRequest{
		EncryptUserName: encryptUserName,
		Ticket:          ticket,
		Scene:           scene,
	})
	return err
}

// SetRemark sets a remark name for a contact.
func (c *Client) SetRemark(ctx context.Context, userName, remark string) error {
	_, err := c.PostJSON(ctx, "/friend/SetRemark", &setRemarkRequest{
		UserName: userName,
		Remark:   remark,
	})
	return err
}

// --- Group API ---

// GetChatRoomInfo fetches group chat info including member list.
func (c *Client) GetChatRoomInfo(ctx context.Context, chatRoomName string) (*chatRoomInfoResponse, error) {
	resp, err := c.PostJSON(ctx, "/group/GetChatRoomInfo", map[string]string{
		"chat_room_name": chatRoomName,
	})
	if err != nil {
		return nil, err
	}
	var data chatRoomInfoResponse
	if err := c.ParseData(resp, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// CreateChatRoom creates a new group chat.
func (c *Client) CreateChatRoom(ctx context.Context, userNames []string) (*createChatRoomResponse, error) {
	resp, err := c.PostJSON(ctx, "/group/CreateChatRoom", &createChatRoomRequest{
		UserNames: userNames,
	})
	if err != nil {
		return nil, err
	}
	var data createChatRoomResponse
	if err := c.ParseData(resp, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// AddChatRoomMembers invites users to a group chat.
func (c *Client) AddChatRoomMembers(ctx context.Context, chatRoomName string, userNames []string) error {
	_, err := c.PostJSON(ctx, "/group/AddChatRoomMembers", &chatRoomMembersRequest{
		ChatRoomName: chatRoomName,
		UserNames:    userNames,
	})
	return err
}

// DelChatRoomMembers removes users from a group chat.
func (c *Client) DelChatRoomMembers(ctx context.Context, chatRoomName string, userNames []string) error {
	_, err := c.PostJSON(ctx, "/group/DelChatRoomMembers", &chatRoomMembersRequest{
		ChatRoomName: chatRoomName,
		UserNames:    userNames,
	})
	return err
}

// SetChatroomName sets the group chat name.
func (c *Client) SetChatroomName(ctx context.Context, chatRoomName, nickName string) error {
	_, err := c.PostJSON(ctx, "/group/SetChatroomName", &setChatRoomNameRequest{
		ChatRoomName: chatRoomName,
		NickName:     nickName,
	})
	return err
}

// SetChatroomAnnouncement sets the group announcement.
func (c *Client) SetChatroomAnnouncement(ctx context.Context, chatRoomName, announcement string) error {
	_, err := c.PostJSON(ctx, "/group/SetChatroomAnnouncement", &setChatRoomAnnouncementRequest{
		ChatRoomName: chatRoomName,
		Announcement: announcement,
	})
	return err
}

// QuitChatRoom leaves a group chat.
func (c *Client) QuitChatRoom(ctx context.Context, chatRoomName string) error {
	_, err := c.PostJSON(ctx, "/group/QuitChatRoom", &quitChatRoomRequest{
		ChatRoomName: chatRoomName,
	})
	return err
}

// --- Webhook API ---

// ConfigureWebhook sets up the webhook callback endpoint.
func (c *Client) ConfigureWebhook(ctx context.Context, webhookURL string) error {
	_, err := c.PostJSON(ctx, "/v1/webhook/Config", &webhookConfigRequest{
		URL:     webhookURL,
		Enabled: true,
	})
	return err
}

// --- Utility ---

// EncodeMediaToBase64 reads all data from a reader and returns base64 string.
func EncodeMediaToBase64(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
