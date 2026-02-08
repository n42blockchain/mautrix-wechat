package bridge

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// ASHandler implements the Matrix Application Service HTTP API.
// It receives transactions from the homeserver containing events and queries.
type ASHandler struct {
	log         *slog.Logger
	hsToken     string // Token that the homeserver uses to authenticate
	eventRouter *EventRouter
	mux         *http.ServeMux
}

// ASTransaction represents a batch of events pushed by the homeserver.
type ASTransaction struct {
	Events []ASEvent `json:"events"`
}

// ASEvent represents a single event in an AS transaction.
type ASEvent struct {
	ID              string                 `json:"event_id"`
	Type            string                 `json:"type"`
	RoomID          string                 `json:"room_id"`
	Sender          string                 `json:"sender"`
	Content         map[string]interface{} `json:"content"`
	OriginServerTS  int64                  `json:"origin_server_ts"`
	Unsigned        map[string]interface{} `json:"unsigned,omitempty"`
	StateKey        *string                `json:"state_key,omitempty"`
}

// NewASHandler creates a new Application Service HTTP handler.
func NewASHandler(log *slog.Logger, hsToken string, router *EventRouter) *ASHandler {
	h := &ASHandler{
		log:         log,
		hsToken:     hsToken,
		eventRouter: router,
		mux:         http.NewServeMux(),
	}
	h.registerRoutes()
	return h
}

func (h *ASHandler) registerRoutes() {
	// Transaction endpoint — receives events from the homeserver
	h.mux.HandleFunc("PUT /transactions/{txnId}", h.handleTransaction)
	h.mux.HandleFunc("PUT /_matrix/app/v1/transactions/{txnId}", h.handleTransaction)

	// User query — homeserver asks if a user exists
	h.mux.HandleFunc("GET /users/{userId}", h.handleUserQuery)
	h.mux.HandleFunc("GET /_matrix/app/v1/users/{userId}", h.handleUserQuery)

	// Room query — homeserver asks if a room alias exists
	h.mux.HandleFunc("GET /rooms/{roomAlias}", h.handleRoomQuery)
	h.mux.HandleFunc("GET /_matrix/app/v1/rooms/{roomAlias}", h.handleRoomQuery)

	// Health check
	h.mux.HandleFunc("GET /_matrix/app/v1/ping", h.handlePing)
	h.mux.HandleFunc("GET /health", h.handlePing)
}

// ServeHTTP implements http.Handler.
func (h *ASHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// authenticate verifies the homeserver token from the request.
func (h *ASHandler) authenticate(r *http.Request) bool {
	token := r.URL.Query().Get("access_token")
	if token == "" {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(h.hsToken)) == 1
}

// handleTransaction processes a transaction of events from the homeserver.
func (h *ASHandler) handleTransaction(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		h.jsonError(w, http.StatusForbidden, "M_FORBIDDEN", "bad token")
		return
	}

	var txn ASTransaction
	if err := json.NewDecoder(r.Body).Decode(&txn); err != nil {
		h.jsonError(w, http.StatusBadRequest, "M_BAD_JSON", "invalid JSON")
		return
	}

	ctx := r.Context()

	for _, evt := range txn.Events {
		matrixEvt := &MatrixEvent{
			ID:        evt.ID,
			Type:      evt.Type,
			RoomID:    evt.RoomID,
			Sender:    evt.Sender,
			Content:   evt.Content,
			Timestamp: evt.OriginServerTS,
		}

		if err := h.eventRouter.HandleMatrixEvent(ctx, matrixEvt); err != nil {
			h.log.Error("failed to handle matrix event",
				"event_id", evt.ID, "type", evt.Type, "error", err)
		}
	}

	h.jsonOK(w)
}

// handleUserQuery responds to user existence queries from the homeserver.
// When the homeserver encounters an unknown user in the appservice namespace,
// it asks the AS whether to lazily create that user.
func (h *ASHandler) handleUserQuery(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		h.jsonError(w, http.StatusForbidden, "M_FORBIDDEN", "bad token")
		return
	}

	userID := r.PathValue("userId")
	if userID == "" {
		h.jsonError(w, http.StatusBadRequest, "M_BAD_REQUEST", "missing user ID")
		return
	}

	// Check if this is one of our puppet users
	if h.eventRouter.puppets.IsPuppet(userID) {
		h.jsonOK(w)
		return
	}

	h.jsonError(w, http.StatusNotFound, "M_NOT_FOUND", "user not found")
}

// handleRoomQuery responds to room alias queries from the homeserver.
func (h *ASHandler) handleRoomQuery(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		h.jsonError(w, http.StatusForbidden, "M_FORBIDDEN", "bad token")
		return
	}

	// We don't use room aliases
	h.jsonError(w, http.StatusNotFound, "M_NOT_FOUND", "room not found")
}

// handlePing responds to health/ping checks.
func (h *ASHandler) handlePing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{}`)
}

func (h *ASHandler) jsonOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{}`)
}

func (h *ASHandler) jsonError(w http.ResponseWriter, status int, errCode, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp, _ := json.Marshal(map[string]string{
		"errcode": errCode,
		"error":   message,
	})
	w.Write(resp)
}
