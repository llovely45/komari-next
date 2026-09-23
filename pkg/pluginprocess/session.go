package pluginprocess

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const maxConcurrentCalls = 64

type MessageHandler func(context.Context, Message) ([]byte, *PluginError)
type EventHandler func(context.Context, Message)

// Session multiplexes host-to-plugin calls, plugin-to-host calls, results,
// and events over one Stream.
type Session struct {
	stream  *Stream
	handler MessageHandler
	event   EventHandler

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan Message
	calls   chan struct{}
}

func NewSession(parent context.Context, stream *Stream, handler MessageHandler, event EventHandler) *Session {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &Session{
		stream: stream, handler: handler, event: event, ctx: ctx, cancel: cancel,
		done: make(chan struct{}), pending: make(map[string]chan Message),
		calls: make(chan struct{}, maxConcurrentCalls),
	}
}

func (s *Session) Serve() error {
	defer close(s.done)
	defer s.cancel()
	for {
		message, err := s.stream.ReadMessage()
		if err != nil {
			if errors.Is(err, io.EOF) || s.ctx.Err() != nil {
				return nil
			}
			return err
		}
		switch message.Type {
		case MessageResult:
			s.deliverResult(message)
		case MessageEvent:
			if s.event != nil {
				s.event(s.ctx, message)
			}
		case MessageCall:
			select {
			case s.calls <- struct{}{}:
				go s.handleCall(message)
			default:
				_ = s.send(Message{Type: MessageResult, RequestID: message.RequestID,
					Error: &PluginError{Code: "busy", Message: "too many concurrent plugin calls", Retryable: true}})
			}
		}
	}
}

func (s *Session) Call(ctx context.Context, method string, payload []byte) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if method == "" {
		return nil, errors.New("plugin call method is empty")
	}
	id, err := s.nextID()
	if err != nil {
		return nil, err
	}
	response := make(chan Message, 1)
	s.mu.Lock()
	select {
	case <-s.done:
		s.mu.Unlock()
		return nil, errors.New("plugin session is closed")
	default:
	}
	s.pending[id] = response
	s.mu.Unlock()
	if err := s.send(Message{Type: MessageCall, RequestID: id, Method: method, Payload: payload}); err != nil {
		s.removePending(id)
		return nil, err
	}
	select {
	case result := <-response:
		if result.Error != nil {
			return nil, result.Error
		}
		return result.Payload, nil
	case <-ctx.Done():
		s.removePending(id)
		return nil, ctx.Err()
	case <-s.done:
		s.removePending(id)
		return nil, errors.New("plugin session closed before returning a result")
	}
}

func (s *Session) Close() {
	s.cancel()
}

// HasPendingCalls reports whether the session is waiting for responses to
// calls sent to the peer. This is useful to distinguish an idle session from
// one whose peer is expected to be processing work.
func (s *Session) HasPendingCalls() bool {
	s.mu.Lock()
	pending := len(s.pending) > 0
	s.mu.Unlock()
	return pending
}

func (s *Session) Notify(method string, payload []byte) error {
	if method == "" {
		return errors.New("plugin event method is empty")
	}
	return s.send(Message{Type: MessageEvent, Method: method, Payload: payload})
}

func (s *Session) handleCall(message Message) {
	defer func() { <-s.calls }()
	if s.handler == nil {
		_ = s.send(Message{Type: MessageResult, RequestID: message.RequestID,
			Error: &PluginError{Code: "method_not_found", Message: "host method not found"}})
		return
	}
	payload, pluginErr := s.handler(s.ctx, message)
	_ = s.send(Message{Type: MessageResult, RequestID: message.RequestID, Payload: payload, Error: pluginErr})
}

func (s *Session) deliverResult(message Message) {
	s.mu.Lock()
	response := s.pending[message.RequestID]
	delete(s.pending, message.RequestID)
	s.mu.Unlock()
	if response != nil {
		response <- message
	}
}

func (s *Session) removePending(id string) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

func (s *Session) send(message Message) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.stream.WriteMessage(message)
}

func (s *Session) nextID() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate plugin request id: %w", err)
	}
	return fmt.Sprintf("%x-%x", time.Now().UnixNano(), random[:]), nil
}

// Client is the small SDK surface used by Go/WASI plugin entry points.
type Client struct {
	stream        *Stream
	session       *Session
	mu            sync.RWMutex
	handlers      map[string]MessageHandler
	methods       []string
	ready         chan error
	cancel        context.CancelFunc
	heartbeatDone chan struct{}
}

type HTTPRequest struct {
	URL     string      `json:"url"`
	Method  string      `json:"method,omitempty"`
	Headers http.Header `json:"headers,omitempty"`
	Body    []byte      `json:"body,omitempty"`
}

type HTTPResponse struct {
	Status  int         `json:"status"`
	Headers http.Header `json:"headers"`
	Body    []byte      `json:"body"`
}

type StorageEntry struct {
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
	Size      int64  `json:"size"`
}

func NewClient() *Client {
	return &Client{handlers: make(map[string]MessageHandler), ready: make(chan error, 1), heartbeatDone: make(chan struct{})}
}

func (c *Client) RegisterRPC(method string, handler MessageHandler) error {
	if method == "" || handler == nil {
		return errors.New("plugin RPC requires a method and handler")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.handlers[method]; exists {
		return fmt.Errorf("plugin RPC %q is already registered", method)
	}
	c.handlers[method] = handler
	c.methods = append(c.methods, method)
	return nil
}

// Start performs the handshake, registers manifest-declared RPC methods with
// the host, and starts the message loop. The caller must register handlers
// before calling Start.
func (c *Client) Start(ctx context.Context, reader io.Reader, writer io.Writer, handshake Handshake) error {
	c.stream = NewStream(reader, writer)
	if err := c.stream.WriteHandshake(handshake); err != nil {
		return err
	}
	response, err := c.stream.ReadHandshakeResponse()
	if err != nil {
		return err
	}
	if !response.Accepted {
		if response.Error != nil {
			return response.Error
		}
		return errors.New("host rejected plugin handshake")
	}
	if response.Handshake == nil {
		return errors.New("host returned an empty plugin handshake")
	}
	accepted := *response.Handshake
	if accepted.ProtocolVersion != ProtocolVersion || accepted.PluginID != handshake.PluginID ||
		accepted.PluginVersion != handshake.PluginVersion || accepted.KomariAPIVersion != handshake.KomariAPIVersion {
		return errors.New("host returned a mismatched plugin handshake")
	}
	approved := make(map[Capability]struct{}, len(accepted.ApprovedCapabilities))
	for _, capability := range accepted.ApprovedCapabilities {
		approved[capability] = struct{}{}
	}
	for _, capability := range handshake.RequestedCapabilities {
		if _, ok := approved[capability]; !ok {
			return fmt.Errorf("host did not approve requested capability %q", capability)
		}
	}
	clientCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.session = NewSession(clientCtx, c.stream, c.handleCall, nil)
	go func() {
		c.ready <- c.session.Serve()
		cancel()
	}()
	go c.heartbeat(clientCtx)
	c.mu.RLock()
	methods := append([]string(nil), c.methods...)
	c.mu.RUnlock()
	payload, err := json.Marshal(map[string]any{"methods": methods})
	if err != nil {
		return fmt.Errorf("encode registered RPC methods: %w", err)
	}
	if _, err := c.session.Call(clientCtx, "host.register_rpc", payload); err != nil {
		return fmt.Errorf("register plugin RPC methods: %w", err)
	}
	return nil
}

func (c *Client) heartbeat(ctx context.Context) {
	defer close(c.heartbeatDone)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if c.session == nil || c.session.Notify("heartbeat", nil) != nil {
				return
			}
		}
	}
}

func (c *Client) CallHost(ctx context.Context, method string, payload any, output any) error {
	if c.session == nil {
		return errors.New("plugin client has not started")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	result, err := c.session.Call(ctx, method, data)
	if err != nil {
		return err
	}
	if output == nil || len(result) == 0 {
		return nil
	}
	if err := json.Unmarshal(result, output); err != nil {
		return fmt.Errorf("decode host response: %w", err)
	}
	return nil
}

// CallKomariRPC calls one admin RPC method explicitly allowed in the
// manifest's goHostRPCMethods list.
func (c *Client) CallKomariRPC(ctx context.Context, method string, params any, output any) error {
	return c.CallHost(ctx, "host.rpc", map[string]any{"method": method, "params": params}, output)
}

// HTTPRequest asks the host to make an HTTPS request using the plugin's
// approved network capability. The host blocks private and reserved IPs.
func (c *Client) HTTPRequest(ctx context.Context, request HTTPRequest) (HTTPResponse, error) {
	var response HTTPResponse
	err := c.CallHost(ctx, "host.http", request, &response)
	return response, err
}

// ReadFile reads a file from this plugin's private persistent storage. Paths
// are relative, and the host rejects traversal and symbolic links.
func (c *Client) ReadFile(ctx context.Context, name string) ([]byte, error) {
	var response struct {
		Data []byte `json:"data"`
	}
	if err := c.CallHost(ctx, "host.storage.read", map[string]string{"path": name}, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

// WriteFile atomically writes a file to this plugin's private storage. Files
// are limited to 2 MiB and the plugin's total storage is limited to 128 MiB.
func (c *Client) WriteFile(ctx context.Context, name string, data []byte) error {
	return c.CallHost(ctx, "host.storage.write", map[string]any{"path": name, "data": data}, nil)
}

func (c *Client) DeleteFile(ctx context.Context, name string) error {
	return c.CallHost(ctx, "host.storage.delete", map[string]string{"path": name}, nil)
}

func (c *Client) ListFiles(ctx context.Context, directory string) ([]StorageEntry, error) {
	var response struct {
		Entries []StorageEntry `json:"entries"`
	}
	if err := c.CallHost(ctx, "host.storage.list", map[string]string{"path": directory}, &response); err != nil {
		return nil, err
	}
	return response.Entries, nil
}

func (c *Client) Log(ctx context.Context, message string) error {
	if c.session == nil {
		return errors.New("plugin client has not started")
	}
	payload, _ := json.Marshal(map[string]string{"message": message})
	_, err := c.session.Call(ctx, "host.log", payload)
	return err
}

func (c *Client) Wait() error {
	if c.session == nil {
		return errors.New("plugin client has not started")
	}
	err := <-c.ready
	if c.cancel != nil {
		c.cancel()
	}
	<-c.heartbeatDone
	return err
}

func (c *Client) handleCall(ctx context.Context, message Message) ([]byte, *PluginError) {
	c.mu.RLock()
	handler := c.handlers[message.Method]
	c.mu.RUnlock()
	if handler == nil {
		return nil, &PluginError{Code: "method_not_found", Message: "plugin method not found"}
	}
	return handler(ctx, message)
}
