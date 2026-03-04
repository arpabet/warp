package warp_ws

import (
	"context"
	"errors"
	"fmt"
	"github.com/arpabet/warp"
	"github.com/arpabet/warp/warp_io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// ─────────────────────────────────────────────────────────────────────────────
// WebSocket warp (uses Serializer to (de)code *warp.WMsg frames)
// ─────────────────────────────────────────────────────────────────────────────

type wsTransport struct {
	mu             sync.RWMutex
	headerProvider func() http.Header
	subprotocols   []string
	nextSessionID  atomic.Int64
}

func NewWebSocketTransport() warp.WebSocketTransport { return &wsTransport{} }

func (w *wsTransport) WithHeaderProvider(fn func() http.Header) warp.WebSocketTransport {
	w.mu.Lock()
	w.headerProvider = fn
	w.mu.Unlock()
	return w
}
func (w *wsTransport) WithSubprotocols(subprotocols ...string) warp.WebSocketTransport {
	w.mu.Lock()
	w.subprotocols = append([]string(nil), subprotocols...)
	w.mu.Unlock()
	return w
}

// Options + cfg
type wsDialOpt func(*wsDialCfg)
type wsListenOpt func(*wsListenCfg)

func (wsDialOpt) IsDialOption()              {}
func (wsDialOpt) IsWebSocketDialOption()     {}
func (wsListenOpt) IsListenOption()          {}
func (wsListenOpt) IsWebSocketListenOption() {}

type wsDialCfg struct {
	serializer     warp.Serializer
	connectTimeout time.Duration
}

type wsListenCfg struct {
	path       string
	serializer warp.Serializer
	upgrader   websocket.Upgrader
}

func WithConnectTimeout(t time.Duration) warp.WebSocketDialOption {
	return wsDialOpt(func(c *wsDialCfg) { c.connectTimeout = t })
}
func WithSerializer(s warp.Serializer) warp.WebSocketDialOption {
	return wsDialOpt(func(c *wsDialCfg) { c.serializer = s })
}
func WithPath(path string) warp.WebSocketListenOption {
	return wsListenOpt(func(c *wsListenCfg) { c.path = path })
}
func WithListenSerializer(s warp.Serializer) warp.WebSocketListenOption {
	return wsListenOpt(func(c *wsListenCfg) { c.serializer = s })
}
func WithUpgrader(u websocket.Upgrader) warp.WebSocketListenOption {
	return wsListenOpt(func(c *wsListenCfg) { c.upgrader = u })
}

func (w *wsTransport) Dial(ctx context.Context, endpoint string, opts ...warp.DialOption) (warp.Connection, error) {
	cfg := &wsDialCfg{serializer: warp_io.NewMsgPackSerializer()}
	for _, o := range opts {
		if oo, ok := o.(wsDialOpt); ok {
			oo(cfg)
		}
	}
	if cfg.connectTimeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.connectTimeout)
		defer cancel()
	}

	u, hdr, sub, err := w.prepareDial(endpoint)
	if err != nil {
		return nil, err
	}

	d := websocket.Dialer{Subprotocols: sub, Proxy: http.ProxyFromEnvironment}
	connCh := make(chan *websocket.Conn, 1)
	errCh := make(chan error, 1)

	go func() {
		c, _, e := d.DialContext(ctx, u, hdr)
		if e != nil {
			errCh <- e
			return
		}
		connCh <- c
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-errCh:
		return nil, err
	case c := <-connCh:
		ws := &wsConnection{
			c:         c,
			ser:       cfg.serializer,
			endpoint:  u,
			sessionID: w.nextSessionID.Add(1),
		}
		w.upgradeDefaults(c)
		ws.startReadLoop()
		return ws, nil
	}
}

func (w *wsTransport) upgradeDefaults(c *websocket.Conn) {
	c.SetReadLimit(1 << 20) // e.g., 1MB
	c.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.SetPongHandler(func(string) error {
		c.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
}

func (w *wsTransport) prepareDial(endpoint string) (string, http.Header, []string, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	u := strings.TrimSpace(endpoint)
	if !strings.HasPrefix(u, "ws://") && !strings.HasPrefix(u, "wss://") {
		return "", nil, nil, fmt.Errorf("endpoint must start with ws:// or wss://: %s", endpoint)
	}
	hdr := http.Header{}
	if w.headerProvider != nil {
		for k, vv := range w.headerProvider() {
			for _, v := range vv {
				hdr.Add(k, v)
			}
		}
	}
	sub := append([]string(nil), w.subprotocols...)
	return u, hdr, sub, nil
}

func (w *wsTransport) Listen(ctx context.Context, endpoint string, acceptor warp.Acceptor, opts ...warp.ListenOption) (warp.Listener, error) {
	cfg := &wsListenCfg{
		path:       "/ws",
		serializer: warp_io.NewMsgPackSerializer(),
		upgrader: websocket.Upgrader{
			CheckOrigin: defaultCheckOrigin,
		},
	}
	for _, o := range opts {
		if oo, ok := o.(wsListenOpt); ok {
			oo(cfg)
		}
	}
	if len(cfg.upgrader.Subprotocols) == 0 {
		cfg.upgrader.Subprotocols = []string{"msgpack", "json"}
	}

	addr, path, err := parseListenEndpoint(endpoint, cfg.path)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	ln := &wsListener{
		addr: addr,
		srv: &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		},
	}

	// Server Upgrader: Upgrader.Subprotocols = []string{"msgpack","json"}
	mux.HandleFunc(path, func(wr http.ResponseWriter, r *http.Request) {
		c, err := cfg.upgrader.Upgrade(wr, r, nil)
		if err != nil {
			return
		}
		w.upgradeDefaults(c)
		ser := cfg.serializer
		switch c.Subprotocol() {
		case "json":
			ser = warp_io.NewJSONSerializer()
		case "msgpack":
			ser = warp_io.NewMsgPackSerializer()
		}
		ws := &wsConnection{
			c:         c,
			ser:       ser,
			endpoint:  "ws://" + r.Host + path,
			sessionID: w.nextSessionID.Add(1),
		}
		if acceptor != nil {
			acceptor.OnAccept(ws)
		}
		ws.startReadLoop()
	})

	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	ln.baseLn = l

	go func() { _ = ln.srv.Serve(l) }()
	go func() { <-ctx.Done(); _ = ln.Close() }()
	return ln, nil
}

func parseListenEndpoint(ep, defaultPath string) (addr, path string, err error) {
	ep = strings.TrimSpace(ep)
	if ep == "" {
		return "", "", errors.New("empty endpoint")
	}
	if strings.HasPrefix(ep, "ws://") || strings.HasPrefix(ep, "wss://") ||
		strings.HasPrefix(ep, "http://") || strings.HasPrefix(ep, "https://") {
		u, e := url.Parse(ep)
		if e != nil {
			return "", "", e
		}
		if u.Host == "" {
			return "", "", fmt.Errorf("missing host:port in %s", ep)
		}
		if u.Path == "" {
			u.Path = defaultPath
		}
		return u.Host, u.Path, nil
	}
	return ep, defaultPath, nil // ":8080" → default path
}

func defaultCheckOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Non-browser clients often omit Origin.
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// ─────────────────────────────────────────────────────────────────────────────
// Connection implementation
// ─────────────────────────────────────────────────────────────────────────────
type wsConnection struct {
	c         *websocket.Conn
	ser       warp.Serializer
	handler   warp.Handler
	endpoint  string
	sessionID int64

	stateMu     sync.RWMutex
	writeMu     sync.Mutex
	closeOnce   sync.Once
	onCloseOnce sync.Once
	closed      chan struct{}
}

func (c *wsConnection) Endpoint() string { return c.endpoint }
func (c *wsConnection) SessionID() int64 { return c.sessionID }
func (c *wsConnection) Serializer() warp.Serializer {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.ser
}
func (c *wsConnection) SetSerializer(s warp.Serializer) {
	c.stateMu.Lock()
	c.ser = s
	c.stateMu.Unlock()
}

func (c *wsConnection) SetHandler(h warp.Handler) {
	c.stateMu.Lock()
	c.handler = h
	c.stateMu.Unlock()
}

func (c *wsConnection) getHandler() warp.Handler {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.handler
}

func (c *wsConnection) Send(ctx context.Context, msg *warp.WMsg) error {
	if msg == nil {
		return errors.New("nil message")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	ser := c.Serializer()
	data, err := ser.Marshal(msg) // encode WMsg with active serializer
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	deadline, ok := ctx.Deadline()
	if ok {
		_ = c.c.SetWriteDeadline(deadline)
		defer c.c.SetWriteDeadline(time.Time{})
	}
	return c.c.WriteMessage(ser.MessageType(), data)
}

func (c *wsConnection) Close() error {
	var err error
	c.closeOnce.Do(func() {
		if c.c != nil {
			err = c.c.Close()
		}
		c.onCloseOnce.Do(func() {
			if h := c.getHandler(); h != nil {
				h.OnClose(c, err)
			}
		})
	})
	return err
}

func (c *wsConnection) startPing(interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				c.writeMu.Lock()
				_ = c.c.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
				c.writeMu.Unlock()
			case <-c.closed:
				return
			}
		}
	}()
}

func (c *wsConnection) startReadLoop() {
	if c.closed == nil {
		c.closed = make(chan struct{})
	}
	go func() {
		defer close(c.closed)
		if h := c.getHandler(); h != nil {
			h.OnOpen(c)
		}
		c.startPing(30 * time.Second)
		for {
			mt, data, err := c.c.ReadMessage()
			if err != nil {
				if h := c.getHandler(); h != nil {
					h.OnError(c, err)
					c.onCloseOnce.Do(func() {
						if h := c.getHandler(); h != nil {
							h.OnClose(c, err)
						}
					})
				}
				return
			}
			current := c.Serializer()
			var deser warp.Serializer
			if mt == current.MessageType() {
				deser = current
			} else {
				if s, ok := warp_io.TryCreateSerializerFor(mt); ok {
					deser = s
					c.SetSerializer(s) // <-- ensure future writes mirror the peer
				} else {
					continue
				}
			}
			var msg warp.WMsg
			if err := deser.Unmarshal(data, &msg); err != nil {
				if h := c.getHandler(); h != nil {
					h.OnError(c, fmt.Errorf("unmarshal: %w", err))
				}
				continue
			}
			if h := c.getHandler(); h != nil {
				h.OnMessage(c, msg)
			}
		}
	}()
}

// ─────────────────────────────────────────────────────────────────────────────
// Listener
// ─────────────────────────────────────────────────────────────────────────────

type wsListener struct {
	addr   string
	baseLn net.Listener
	srv    *http.Server
}

func (l *wsListener) Addr() string { return l.addr }
func (l *wsListener) Close() error {
	if l.srv != nil {
		_ = l.srv.Close()
	}
	if l.baseLn != nil {
		return l.baseLn.Close()
	}
	return nil
}
