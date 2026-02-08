package wecom

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// CallbackServer handles incoming webhook callbacks from WeCom.
// It handles both URL verification (GET) and event notifications (POST).
type CallbackServer struct {
	log     *slog.Logger
	crypto  *CallbackCrypto
	handler wechat.MessageHandler
	server  *http.Server
}

// NewCallbackServer creates a new WeCom callback HTTP server.
func NewCallbackServer(log *slog.Logger, crypto *CallbackCrypto, handler wechat.MessageHandler) *CallbackServer {
	return &CallbackServer{
		log:     log,
		crypto:  crypto,
		handler: handler,
	}
}

// Start begins listening for callbacks on the given port.
func (cs *CallbackServer) Start(port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", cs.handleRequest)
	mux.HandleFunc("/callback/", cs.handleRequest)

	cs.server = &http.Server{
		Addr:           fmt.Sprintf("0.0.0.0:%d", port),
		Handler:        mux,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1MB
	}

	cs.log.Info("wecom callback server starting", "port", port)
	go func() {
		if err := cs.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			cs.log.Error("callback server error", "error", err)
		}
	}()

	return nil
}

// Stop gracefully shuts down the callback server.
func (cs *CallbackServer) Stop() error {
	if cs.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return cs.server.Shutdown(ctx)
	}
	return nil
}

// handleRequest dispatches GET (URL verification) and POST (event callbacks).
func (cs *CallbackServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cs.handleVerification(w, r)
	case http.MethodPost:
		cs.handleEvent(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleVerification handles the URL verification callback from WeCom.
// WeCom sends: GET /callback?msg_signature=xxx&timestamp=xxx&nonce=xxx&echostr=xxx
// We must decrypt echostr and return the plaintext to confirm.
func (cs *CallbackServer) handleVerification(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	msgSignature := q.Get("msg_signature")
	timestamp := q.Get("timestamp")
	nonce := q.Get("nonce")
	echoStr := q.Get("echostr")

	if msgSignature == "" || timestamp == "" || nonce == "" || echoStr == "" {
		cs.log.Warn("verification missing parameters")
		http.Error(w, "missing parameters", http.StatusBadRequest)
		return
	}

	// Verify signature
	if !cs.crypto.VerifySignature(msgSignature, timestamp, nonce, echoStr) {
		cs.log.Warn("verification signature mismatch")
		http.Error(w, "signature mismatch", http.StatusForbidden)
		return
	}

	// Decrypt echostr
	plaintext, _, err := cs.crypto.DecryptMessage(echoStr)
	if err != nil {
		cs.log.Error("decrypt echostr failed", "error", err)
		http.Error(w, "decrypt failed", http.StatusInternalServerError)
		return
	}

	cs.log.Info("url verification successful")

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write(plaintext)
}

// handleEvent processes incoming event callbacks from WeCom.
// WeCom sends: POST /callback?msg_signature=xxx&timestamp=xxx&nonce=xxx
// Body is XML with encrypted content.
func (cs *CallbackServer) handleEvent(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	msgSignature := q.Get("msg_signature")
	timestamp := q.Get("timestamp")
	nonce := q.Get("nonce")

	if msgSignature == "" || timestamp == "" || nonce == "" {
		http.Error(w, "missing parameters", http.StatusBadRequest)
		return
	}

	// Limit request body to 1MB to prevent memory exhaustion attacks
	const maxBodySize = 1 << 20 // 1MB
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse outer XML
	var encXML CallbackEncryptedXML
	if err := xml.Unmarshal(body, &encXML); err != nil {
		cs.log.Error("parse encrypted xml", "error", err)
		http.Error(w, "bad xml", http.StatusBadRequest)
		return
	}

	// Verify signature
	if !cs.crypto.VerifySignature(msgSignature, timestamp, nonce, encXML.Encrypt) {
		cs.log.Warn("event signature mismatch")
		http.Error(w, "signature mismatch", http.StatusForbidden)
		return
	}

	// Decrypt
	plaintext, _, err := cs.crypto.DecryptMessage(encXML.Encrypt)
	if err != nil {
		cs.log.Error("decrypt event message", "error", err)
		http.Error(w, "decrypt failed", http.StatusInternalServerError)
		return
	}

	// Parse decrypted XML
	var msg CallbackMessage
	if err := xml.Unmarshal(plaintext, &msg); err != nil {
		cs.log.Error("parse decrypted xml", "error", err)
		http.Error(w, "parse failed", http.StatusInternalServerError)
		return
	}

	// Dispatch the message/event
	ctx := r.Context()
	if err := cs.dispatch(ctx, &msg); err != nil {
		cs.log.Error("dispatch callback event",
			"error", err, "type", msg.MsgType, "event", msg.Event)
	}

	// Respond with success (WeCom expects "success" or empty response)
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "success")
}

// dispatch routes a decrypted callback message to the appropriate handler.
func (cs *CallbackServer) dispatch(ctx context.Context, msg *CallbackMessage) error {
	switch msg.MsgType {
	case "text":
		return cs.dispatchTextMessage(ctx, msg)
	case "image":
		return cs.dispatchImageMessage(ctx, msg)
	case "voice":
		return cs.dispatchVoiceMessage(ctx, msg)
	case "video", "shortvideo":
		return cs.dispatchVideoMessage(ctx, msg)
	case "location":
		return cs.dispatchLocationMessage(ctx, msg)
	case "link":
		return cs.dispatchLinkMessage(ctx, msg)
	case "event":
		return cs.dispatchEvent(ctx, msg)
	default:
		cs.log.Debug("unsupported callback message type", "type", msg.MsgType)
		return nil
	}
}

func (cs *CallbackServer) dispatchTextMessage(ctx context.Context, msg *CallbackMessage) error {
	cs.log.Info("received text message",
		"from", msg.FromUserName, "msg_id", msg.MsgID)

	return cs.handler.OnMessage(ctx, &wechat.Message{
		MsgID:     strconv.FormatInt(msg.MsgID, 10),
		Type:      wechat.MsgText,
		FromUser:  msg.FromUserName,
		ToUser:    msg.ToUserName,
		Content:   msg.Content,
		Timestamp: msg.CreateTime * 1000,
	})
}

func (cs *CallbackServer) dispatchImageMessage(ctx context.Context, msg *CallbackMessage) error {
	cs.log.Info("received image message",
		"from", msg.FromUserName, "msg_id", msg.MsgID)

	return cs.handler.OnMessage(ctx, &wechat.Message{
		MsgID:     strconv.FormatInt(msg.MsgID, 10),
		Type:      wechat.MsgImage,
		FromUser:  msg.FromUserName,
		ToUser:    msg.ToUserName,
		MediaURL:  msg.PicURL,
		Timestamp: msg.CreateTime * 1000,
		Extra: map[string]string{
			"media_id": msg.MediaID,
		},
	})
}

func (cs *CallbackServer) dispatchVoiceMessage(ctx context.Context, msg *CallbackMessage) error {
	cs.log.Info("received voice message",
		"from", msg.FromUserName, "msg_id", msg.MsgID)

	wxMsg := &wechat.Message{
		MsgID:     strconv.FormatInt(msg.MsgID, 10),
		Type:      wechat.MsgVoice,
		FromUser:  msg.FromUserName,
		ToUser:    msg.ToUserName,
		Timestamp: msg.CreateTime * 1000,
		Extra: map[string]string{
			"media_id": msg.MediaID,
			"format":   msg.Format,
		},
	}

	// If speech recognition is enabled, include the recognized text
	if msg.Recognition != "" {
		wxMsg.Content = msg.Recognition
	}

	return cs.handler.OnMessage(ctx, wxMsg)
}

func (cs *CallbackServer) dispatchVideoMessage(ctx context.Context, msg *CallbackMessage) error {
	cs.log.Info("received video message",
		"from", msg.FromUserName, "msg_id", msg.MsgID)

	return cs.handler.OnMessage(ctx, &wechat.Message{
		MsgID:     strconv.FormatInt(msg.MsgID, 10),
		Type:      wechat.MsgVideo,
		FromUser:  msg.FromUserName,
		ToUser:    msg.ToUserName,
		Timestamp: msg.CreateTime * 1000,
		Extra: map[string]string{
			"media_id":       msg.MediaID,
			"thumb_media_id": msg.ThumbMediaID,
		},
	})
}

func (cs *CallbackServer) dispatchLocationMessage(ctx context.Context, msg *CallbackMessage) error {
	cs.log.Info("received location message",
		"from", msg.FromUserName, "msg_id", msg.MsgID)

	return cs.handler.OnMessage(ctx, &wechat.Message{
		MsgID:     strconv.FormatInt(msg.MsgID, 10),
		Type:      wechat.MsgLocation,
		FromUser:  msg.FromUserName,
		ToUser:    msg.ToUserName,
		Timestamp: msg.CreateTime * 1000,
		Location: &wechat.LocationInfo{
			Latitude:  msg.LocationX,
			Longitude: msg.LocationY,
			Label:     msg.Label,
		},
	})
}

func (cs *CallbackServer) dispatchLinkMessage(ctx context.Context, msg *CallbackMessage) error {
	cs.log.Info("received link message",
		"from", msg.FromUserName, "msg_id", msg.MsgID)

	return cs.handler.OnMessage(ctx, &wechat.Message{
		MsgID:     strconv.FormatInt(msg.MsgID, 10),
		Type:      wechat.MsgLink,
		FromUser:  msg.FromUserName,
		ToUser:    msg.ToUserName,
		Content:   msg.Title,
		Timestamp: msg.CreateTime * 1000,
		LinkInfo: &wechat.LinkCardInfo{
			Title:       msg.Title,
			Description: msg.Description,
			URL:         msg.URL,
		},
	})
}

// dispatchEvent handles WeCom event callbacks (subscribe, click, external contact, etc.).
func (cs *CallbackServer) dispatchEvent(ctx context.Context, msg *CallbackMessage) error {
	cs.log.Info("received event",
		"event", msg.Event, "change_type", msg.ChangeType,
		"from", msg.FromUserName)

	switch msg.Event {
	case "subscribe":
		// User subscribed to the app
		return cs.handler.OnContactUpdate(ctx, &wechat.ContactInfo{
			UserID:   msg.FromUserName,
			Nickname: msg.FromUserName,
		})

	case "unsubscribe":
		// User unsubscribed from the app
		cs.log.Info("user unsubscribed", "user", msg.FromUserName)
		return nil

	case "change_external_contact":
		return cs.handleExternalContactEvent(ctx, msg)

	case "change_external_chat":
		return cs.handleExternalChatEvent(ctx, msg)

	case "sys_approval_change":
		cs.log.Info("approval event", "change_type", msg.ChangeType)
		return nil

	default:
		cs.log.Debug("unhandled event type", "event", msg.Event)
		return nil
	}
}

// handleExternalContactEvent handles external contact change events.
func (cs *CallbackServer) handleExternalContactEvent(ctx context.Context, msg *CallbackMessage) error {
	switch msg.ChangeType {
	case "add_external_contact":
		// New external contact added
		cs.log.Info("new external contact",
			"user_id", msg.UserID, "external_user_id", msg.ExternalUserID)
		return cs.handler.OnContactUpdate(ctx, &wechat.ContactInfo{
			UserID:   msg.ExternalUserID,
			Nickname: msg.ExternalUserID, // Will be resolved by contact sync
		})

	case "del_external_contact", "del_follow_user":
		// External contact removed
		cs.log.Info("external contact removed",
			"user_id", msg.UserID, "external_user_id", msg.ExternalUserID)
		return nil

	case "edit_external_contact":
		// External contact info changed
		return cs.handler.OnContactUpdate(ctx, &wechat.ContactInfo{
			UserID:   msg.ExternalUserID,
			Nickname: msg.ExternalUserID,
		})

	case "msg_audit_approved":
		// Message audit approved
		cs.log.Info("message audit approved")
		return nil

	default:
		cs.log.Debug("unhandled external contact change", "change_type", msg.ChangeType)
		return nil
	}
}

// handleExternalChatEvent handles external group chat change events.
func (cs *CallbackServer) handleExternalChatEvent(ctx context.Context, msg *CallbackMessage) error {
	switch msg.ChangeType {
	case "create":
		cs.log.Info("external chat created", "chat_id", msg.ChatID)
	case "update":
		cs.log.Info("external chat updated",
			"chat_id", msg.ChatID, "detail", msg.UpdateDetail)
	case "dismiss":
		cs.log.Info("external chat dismissed", "chat_id", msg.ChatID)
	}
	return nil
}
