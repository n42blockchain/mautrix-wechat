package pchook

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// RPCRequest represents a JSON-RPC request sent to WeChatFerry.
type RPCRequest struct {
	ID     uint64      `json:"id"`
	Method string      `json:"method"`
	Params interface{} `json:"params,omitempty"`
}

// RPCResponse represents a JSON-RPC response from WeChatFerry.
type RPCResponse struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

// RPCError represents an error in the JSON-RPC response.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// RPCNotification is a push notification from WeChatFerry (e.g. incoming message).
type RPCNotification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// RPCClient manages a TCP connection to a WeChatFerry RPC server.
type RPCClient struct {
	mu       sync.Mutex
	endpoint string
	conn     net.Conn
	reader   *bufio.Reader
	log      *slog.Logger
	nextID   atomic.Uint64

	// Pending requests waiting for responses
	pending   map[uint64]chan *RPCResponse
	pendingMu sync.Mutex

	// Notification handler
	onNotification func(method string, params json.RawMessage)

	closed  chan struct{}
	running bool
}

// NewRPCClient creates a new RPC client.
func NewRPCClient(endpoint string, log *slog.Logger) *RPCClient {
	return &RPCClient{
		endpoint: endpoint,
		log:      log,
		pending:  make(map[uint64]chan *RPCResponse),
		closed:   make(chan struct{}),
	}
}

// Connect establishes a TCP connection to the WeChatFerry RPC server.
func (c *RPCClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return nil
	}

	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", c.endpoint)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", c.endpoint, err)
	}

	c.conn = conn
	c.reader = bufio.NewReader(conn)
	c.running = true
	c.closed = make(chan struct{})

	go c.readLoop()

	c.log.Info("connected to WeChatFerry RPC", "endpoint", c.endpoint)
	return nil
}

// Close shuts down the RPC client.
func (c *RPCClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return nil
	}

	c.running = false
	close(c.closed)

	// Cancel all pending requests
	c.pendingMu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()

	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// IsConnected returns whether the client has an active connection.
func (c *RPCClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

// SetNotificationHandler registers a callback for push notifications.
func (c *RPCClient) SetNotificationHandler(handler func(method string, params json.RawMessage)) {
	c.onNotification = handler
}

// Call sends an RPC request and waits for the response.
func (c *RPCClient) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	req := RPCRequest{
		ID:     id,
		Method: method,
		Params: params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')

	// Register pending response
	respCh := make(chan *RPCResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = respCh
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	// Send request
	c.mu.Lock()
	if c.conn == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("not connected")
	}
	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err = c.conn.Write(data)
	c.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Wait for response
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp, ok := <-respCh:
		if !ok {
			return nil, fmt.Errorf("connection closed")
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("rpc call %s timed out", method)
	}
}

// readLoop continuously reads responses and notifications from the server.
func (c *RPCClient) readLoop() {
	defer func() {
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
	}()

	for {
		select {
		case <-c.closed:
			return
		default:
		}

		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			select {
			case <-c.closed:
				return
			default:
			}
			c.log.Error("read error", "error", err)
			return
		}

		c.handleIncoming(line)
	}
}

// handleIncoming dispatches a received JSON message.
func (c *RPCClient) handleIncoming(data []byte) {
	// Try to parse as response (has "id" field)
	var resp RPCResponse
	if err := json.Unmarshal(data, &resp); err == nil && resp.ID > 0 {
		c.pendingMu.Lock()
		ch, ok := c.pending[resp.ID]
		c.pendingMu.Unlock()

		if ok {
			ch <- &resp
		}
		return
	}

	// Try to parse as notification (no "id", has "method")
	var notif RPCNotification
	if err := json.Unmarshal(data, &notif); err == nil && notif.Method != "" {
		if c.onNotification != nil {
			go c.onNotification(notif.Method, notif.Params)
		}
		return
	}

	c.log.Warn("received unknown message from RPC server", "data", string(data))
}

// Ping sends a heartbeat to check connection health.
func (c *RPCClient) Ping(ctx context.Context) error {
	result, err := c.Call(ctx, "ping", nil)
	if err != nil {
		return err
	}

	var pong string
	if err := json.Unmarshal(result, &pong); err != nil {
		return fmt.Errorf("unexpected ping response: %s", string(result))
	}

	return nil
}
