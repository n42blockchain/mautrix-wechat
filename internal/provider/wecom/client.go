package wecom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"sync"
	"time"
)

const (
	baseURL            = "https://qyapi.weixin.qq.com"
	tokenExpireBuffer  = 300 // refresh 5 minutes before actual expiry
	maxRetries         = 2
	errCodeTokenExpired = 42001
	errCodeTokenInvalid = 40014
)

// Client wraps the WeCom REST API with automatic token management.
type Client struct {
	mu          sync.RWMutex
	corpID      string
	appSecret   string
	agentID     int
	accessToken string
	tokenExpiry time.Time
	httpClient  *http.Client
	log         *slog.Logger
}

// APIResponse is the common response envelope for all WeCom API calls.
type APIResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

// tokenResponse is the response from /cgi-bin/gettoken.
type tokenResponse struct {
	APIResponse
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// NewClient creates a WeCom API client.
func NewClient(corpID, appSecret string, agentID int, log *slog.Logger) *Client {
	return &Client{
		corpID:    corpID,
		appSecret: appSecret,
		agentID:   agentID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		log: log,
	}
}

// GetToken returns a valid access token, refreshing if necessary.
func (c *Client) GetToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	if c.accessToken != "" && time.Now().Before(c.tokenExpiry) {
		token := c.accessToken
		c.mu.RUnlock()
		return token, nil
	}
	c.mu.RUnlock()

	return c.refreshToken(ctx)
}

// refreshToken fetches a new access token from WeCom.
func (c *Client) refreshToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.accessToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.accessToken, nil
	}

	url := fmt.Sprintf("%s/cgi-bin/gettoken?corpid=%s&corpsecret=%s",
		baseURL, c.corpID, c.appSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch access token: %w", err)
	}
	defer resp.Body.Close()

	var result tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	if result.ErrCode != 0 {
		return "", fmt.Errorf("get token failed: [%d] %s", result.ErrCode, result.ErrMsg)
	}

	c.accessToken = result.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(result.ExpiresIn-tokenExpireBuffer) * time.Second)

	c.log.Info("access token refreshed", "expires_in", result.ExpiresIn)

	return c.accessToken, nil
}

// invalidateToken clears the current token, forcing a refresh on next use.
func (c *Client) invalidateToken() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.accessToken = ""
	c.tokenExpiry = time.Time{}
}

// Get performs an authenticated GET request to the WeCom API.
func (c *Client) Get(ctx context.Context, path string, result interface{}) error {
	return c.doWithRetry(ctx, http.MethodGet, path, nil, "", result)
}

// PostJSON performs an authenticated POST request with a JSON body.
func (c *Client) PostJSON(ctx context.Context, path string, body interface{}, result interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request body: %w", err)
	}
	return c.doWithRetry(ctx, http.MethodPost, path, bytes.NewReader(data), "application/json", result)
}

// UploadMedia uploads a media file and returns the response.
func (c *Client) UploadMedia(ctx context.Context, mediaType string, filename string, data io.Reader) (*MediaUploadResponse, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("media", filename)
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}

	if _, err := io.Copy(part, data); err != nil {
		return nil, fmt.Errorf("copy media data: %w", err)
	}
	writer.Close()

	path := fmt.Sprintf("/cgi-bin/media/upload?type=%s", mediaType)

	var result MediaUploadResponse
	if err := c.doWithRetry(ctx, http.MethodPost, path, &buf, writer.FormDataContentType(), &result); err != nil {
		return nil, err
	}

	if result.ErrCode != 0 {
		return nil, fmt.Errorf("upload media: [%d] %s", result.ErrCode, result.ErrMsg)
	}

	return &result, nil
}

// DownloadMedia downloads a media file by media_id.
func (c *Client) DownloadMedia(ctx context.Context, mediaID string) (io.ReadCloser, string, error) {
	token, err := c.GetToken(ctx)
	if err != nil {
		return nil, "", err
	}

	url := fmt.Sprintf("%s/cgi-bin/media/get?access_token=%s&media_id=%s",
		baseURL, token, mediaID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create download request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download media: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")

	// If response is JSON, it's an error
	if contentType == "application/json" || contentType == "text/plain" {
		defer resp.Body.Close()
		var apiResp APIResponse
		json.NewDecoder(resp.Body).Decode(&apiResp)
		return nil, "", fmt.Errorf("download media failed: [%d] %s", apiResp.ErrCode, apiResp.ErrMsg)
	}

	return resp.Body, contentType, nil
}

// doWithRetry executes an API request with automatic token retry on expiry.
func (c *Client) doWithRetry(ctx context.Context, method, path string, body io.Reader, contentType string, result interface{}) error {
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = io.ReadAll(body)
		if err != nil {
			return fmt.Errorf("read request body: %w", err)
		}
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		token, err := c.GetToken(ctx)
		if err != nil {
			return fmt.Errorf("get token: %w", err)
		}

		// Build URL with access_token
		separator := "?"
		if containsQuery(path) {
			separator = "&"
		}
		url := fmt.Sprintf("%s%s%saccess_token=%s", baseURL, path, separator, token)

		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}

		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("read response: %w", err)
		}

		// Check for token expiry errors
		var apiResp APIResponse
		json.Unmarshal(respBody, &apiResp)

		if apiResp.ErrCode == errCodeTokenExpired || apiResp.ErrCode == errCodeTokenInvalid {
			c.log.Warn("access token expired, refreshing",
				"errcode", apiResp.ErrCode, "attempt", attempt)
			c.invalidateToken()
			continue
		}

		if result != nil {
			if err := json.Unmarshal(respBody, result); err != nil {
				return fmt.Errorf("decode response from %s: %w", path, err)
			}
		}

		return nil
	}

	return fmt.Errorf("max retries exceeded for %s %s", method, path)
}

func containsQuery(path string) bool {
	for _, c := range path {
		if c == '?' {
			return true
		}
	}
	return false
}

// MediaUploadResponse is returned by the media upload API.
type MediaUploadResponse struct {
	APIResponse
	Type      string `json:"type"`
	MediaID   string `json:"media_id"`
	CreatedAt string `json:"created_at"`
}
