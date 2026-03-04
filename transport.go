package warp

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Transport abstraction
// ─────────────────────────────────────────────────────────────────────────────

// Transport is a factory for wclient connections and/or servers.
type Transport interface {
	// Dial creates a wclient connection to a remote endpoint (e.g., ws://host:port/path).
	Dial(ctx context.Context, endpoint string, opts ...DialOption) (Connection, error)

	// Listen starts a wserver listener at a local endpoint (e.g., :8080, ws path).
	// The returned Listener manages lifecycle; incoming connections will be delivered
	// to the provided Acceptor.
	Listen(ctx context.Context, endpoint string, acceptor Acceptor, opts ...ListenOption) (Listener, error)
}

// Connection is a bidirectional channel for Envelopes.
// It owns a Serializer used for encode/decode of Envelope.Payload().
type Connection interface {

	// SessionID returns the unique number of the session, good for indexing
	SessionID() int64

	// Send encodes and transmits an Envelope using the connection’s Serializer.
	Send(ctx context.Context, msg *WMsg) error

	// SetHandler attaches/updates the inbound message handler.
	SetHandler(h Handler)

	// Serializer returns the active serializer.
	Serializer() Serializer

	// SetSerializer switches the serializer (e.g., after a negotiation).
	SetSerializer(s Serializer)

	// Close closes the connection.
	Close() error

	// Endpoint returns the remote endpoint string (for diagnostics).
	Endpoint() string
}

type PerformanceMonitor func(name string, elapsed int64)

type ResponseHandler func(resp *WMsg) bool

type Client interface {
	Transport() Transport

	Connection() Connection

	Reconnect() error

	AddHandler(ResponseHandler)

	WriteCtrl(msg *WMsg) error

	DataPipe() chan<- *WMsg

	Call(ctx context.Context, method string, payload json.RawMessage, timeoutMls int64) (*WMsg, error)

	Subscribe(ctx context.Context, method string, payload json.RawMessage, receiveCap int, firstTimeoutMls int64) (
		firstResponse *WMsg, sequentialResponses <-chan *WMsg, requestId int64, err error)

	CancelRequest(ctx context.Context, requestId int64)

	SetMonitor(perfMonitor PerformanceMonitor)

	Done() <-chan struct{}

	Close() error

	SetWriteTimeout(timeout time.Duration)
}

type Handler interface {
	OnOpen(conn Connection)
	OnMessage(conn Connection, msg WMsg)
	OnError(conn Connection, err error)
	OnClose(conn Connection, err error)
}

type HandlerFuncs struct {
	OnOpenFunc    func(Connection)
	OnMessageFunc func(Connection, WMsg)
	OnErrorFunc   func(Connection, error)
	OnCloseFunc   func(Connection, error)
}

func (h *HandlerFuncs) OnOpen(c Connection) {
	if h != nil && h.OnOpenFunc != nil {
		h.OnOpenFunc(c)
	}
}
func (h *HandlerFuncs) OnMessage(c Connection, m WMsg) {
	if h != nil && h.OnMessageFunc != nil {
		h.OnMessageFunc(c, m)
	}
}
func (h *HandlerFuncs) OnError(c Connection, err error) {
	if h != nil && h.OnErrorFunc != nil {
		h.OnErrorFunc(c, err)
	}
}
func (h *HandlerFuncs) OnClose(c Connection, err error) {
	if h != nil && h.OnCloseFunc != nil {
		h.OnCloseFunc(c, err)
	}
}

// Acceptor is notified of new inbound connections on a Listener.
type Acceptor interface {
	// OnAccept is called for each new connection. Implementations typically call
	// conn.SetHandler(...) and return.
	OnAccept(conn Connection)
}

// Listener represents a running wserver-side warp listener.
type Listener interface {
	// Addr returns the bound address/endpoint.
	Addr() string

	// Close stops the listener (and typically all active child connections).
	Close() error
}

// ─────────────────────────────────────────────────────────────────────────────
// Options (warp-agnostic “marker” interfaces to avoid leaking impls)
// ─────────────────────────────────────────────────────────────────────────────

type DialOption interface{ IsDialOption() }
type ListenOption interface{ IsListenOption() }

// ─────────────────────────────────────────────────────────────────────────────
// 5) WebSocket specialization (sub-case of Transport)
//    — still interface-only, uses Serializer; impl will default to MsgPack.
// ─────────────────────────────────────────────────────────────────────────────

// WebSocketTransport exposes WS-specific knobs while remaining a Transport.
// A concrete implementation will satisfy both Transport and WebSocketTransport.
type WebSocketTransport interface {
	Transport

	// WithHeaderProvider lets clients supply headers during Dial (e.g., auth).
	WithHeaderProvider(fn func() http.Header) WebSocketTransport

	// WithSubprotocols sets desired WS subprotocols for negotiation.
	WithSubprotocols(subprotocols ...string) WebSocketTransport
}

// WebSocketDialOption are DialOptions recognized by WebSocket transports.
type WebSocketDialOption interface {
	DialOption
	IsWebSocketDialOption()
}

// WebSocketListenOption are ListenOptions recognized by WebSocket transports.
type WebSocketListenOption interface {
	ListenOption
	IsWebSocketListenOption()
}

// ─────────────────────────────────────────────────────────────────────────────
// 6) Serializer negotiation (optional, for future extensibility)
// ─────────────────────────────────────────────────────────────────────────────

// Negotiator allows a Transport/Connection to agree on a serializer at runtime
// (e.g., via HTTP headers or WS subprotocols). Implementations are optional.
type Negotiator interface {
	// Propose returns preferred serializers in order (e.g., MsgPack first).
	Propose() []Serializer

	// Select chooses one from the peer’s offered/proposed list.
	Select(peerOffered []string) (Serializer, error)
}
