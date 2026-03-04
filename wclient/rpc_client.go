package wclient

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"morpheus-poc/warp"
	"sync"
	"time"

	"go.uber.org/atomic"
)

type rpcClient struct {
	warp.HandlerFuncs

	mu sync.Mutex

	clientCtx    context.Context // context how long the whole wclient should live, usually application context time-based
	writeTimeout time.Duration

	endpoint string
	opts     []warp.DialOption

	transport warp.Transport
	conn      warp.Connection
	epoch     atomic.Int64

	handlers atomic.Value // []ResponseHandler

	ctrlQ chan *warp.WMsg // control/signaling (high priority)
	dataQ chan *warp.WMsg

	nextRequestId atomic.Int64
	requestCtxMap sync.Map /// seq -> *requestCtx

	perfMonitor  atomic.Value
	shuttingDown atomic.Bool

	closeOnce sync.Once
	closed    chan struct{}
}

// NewWebSocketTransport().WithSubprotocols("msgpack", "json")

func NewClient(clientCtx context.Context, transport warp.Transport, endpoint string, opts ...warp.DialOption) (warp.Client, error) {
	conn, err := transport.Dial(clientCtx, endpoint, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to dial endpoint %s: %w", endpoint, err)
	}
	client := &rpcClient{
		clientCtx:    clientCtx,
		writeTimeout: time.Second,
		endpoint:     endpoint,
		opts:         opts,
		transport:    transport,
		conn:         conn,
		ctrlQ:        make(chan *warp.WMsg, 64), // control/signaling (high priority)
		dataQ:        make(chan *warp.WMsg, 1024),
		closed:       make(chan struct{}),
	}
	client.AddHandler(client.getRPCHandler()) // going to be first in the list
	client.OnMessageFunc = func(connection warp.Connection, msg warp.WMsg) {
		for _, handler := range client.GetHandlers() {
			m := msg
			if handler(&m) {
				break
			}
		}
	}
	client.OnErrorFunc = func(connection warp.Connection, err error) {
		client.doReconnect(err)
	}
	conn.SetHandler(client)
	client.startWriter()
	return client, nil
}

func (c *rpcClient) SetWriteTimeout(timeout time.Duration) {
	c.writeTimeout = timeout
}

func (c *rpcClient) startWriter() {
	handleWriteErr := func(where string, err error) {
		log.Println(where, "write:", err)
		c.doReconnect(err)                // try to repair the link
		time.Sleep(50 * time.Millisecond) // small backoff to avoid spin
	}

	go func() {
		for {
			// first drain control
			for {
				select {
				case <-c.closed:
					return
				case m := <-c.ctrlQ:
					if err := c.writeWithTimeout(m, c.writeTimeout); err != nil {
						handleWriteErr("ctrl", err)
						continue // keep draining/trying
					}
				default:
					goto afterDrain
				}
			}
		afterDrain:
			select {
			case <-c.closed:
				return
			case m := <-c.ctrlQ:
				if err := c.writeWithTimeout(m, c.writeTimeout); err != nil {
					handleWriteErr("ctrl", err)
					continue
				}
			case m := <-c.dataQ:
				if err := c.writeWithTimeout(m, c.writeTimeout); err != nil {
					handleWriteErr("data", err)
					continue
				}
			case <-c.clientCtx.Done():
				return
			}
		}
	}()

}

func (c *rpcClient) AddHandler(handler warp.ResponseHandler) {
	oldHandlers := c.handlers.Load()
	if oldHandlers == nil {
		c.handlers.Store([]warp.ResponseHandler{handler})
		return
	}
	list, ok := oldHandlers.([]warp.ResponseHandler)
	if !ok {
		c.handlers.Store([]warp.ResponseHandler{handler})
		return
	}
	newList := make([]warp.ResponseHandler, len(list))
	// copy on write
	copy(newList, list)
	c.handlers.Store(append(newList, handler))
}

func (c *rpcClient) GetHandlers() []warp.ResponseHandler {
	oldHandlers := c.handlers.Load()
	if oldHandlers == nil {
		return nil
	}
	list, ok := oldHandlers.([]warp.ResponseHandler)
	if !ok {
		return nil
	}
	return list
}

func (c *rpcClient) Transport() warp.Transport {
	return c.transport
}

func (c *rpcClient) Connection() warp.Connection {
	return c.conn
}

func (c *rpcClient) Reconnect() error {
	startedEpoch := c.epoch.Load()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.epoch.Load() > startedEpoch {
		// already concurrent process reconnected
		return nil
	}
	c.epoch.Inc()

	old := c.conn
	newConn, err := c.transport.Dial(context.Background(), c.endpoint, c.opts...)
	if err != nil {
		return fmt.Errorf("failed to dial endpoint %s: %w", c.endpoint, err)
	}
	newConn.SetHandler(c)
	c.conn = newConn
	if old != nil {
		_ = old.Close()
	}
	return nil
}

func (c *rpcClient) Done() <-chan struct{} {
	return c.closed
}

func (c *rpcClient) Close() (err error) {
	c.closeOnce.Do(func() {
		c.shuttingDown.Store(true)
		c.failAllPending(ErrNoResponse)
		err = c.conn.Close()
		close(c.closed)
	})
	return
}

func (c *rpcClient) failAllPending(err error) {
	c.requestCtxMap.Range(func(key, val any) bool {
		ctx := val.(*rpcRequestCtx)
		ctx.SetError(err)
		ctx.Close()
		c.requestCtxMap.Delete(key)
		return true
	})
}

func (c *rpcClient) doReconnect(err error) {
	if c.shuttingDown.Load() {
		return
	}
	if c.clientCtx.Err() != nil {
		return
	}
	c.failAllPending(err) // <— important
	// optional: jitter/backoff here
	if e := c.Reconnect(); e != nil {
		log.Printf("ERROR: reconnect failed, %v\n", e)
	}
}

func (c *rpcClient) addRequestCtx(requestId int64, msg *warp.WMsg, receiveCap int) *rpcRequestCtx {
	requestCtx := newRequestCtx(requestId, msg, receiveCap)
	c.requestCtxMap.Store(requestId, requestCtx)
	return requestCtx
}

func (c *rpcClient) allocateRequestId() int64 {
	return c.nextRequestId.Add(1)
}

func (c *rpcClient) WriteCtrl(msg *warp.WMsg) error {
	select {
	case c.ctrlQ <- msg:
	default:
		// control queue shouldn't fill, but if it does, fall back to direct write:
		return c.writeWithTimeout(msg, c.writeTimeout)
	}
	return nil
}

func (c *rpcClient) DataPipe() chan<- *warp.WMsg {
	return c.dataQ
}

func (c *rpcClient) writeWithTimeout(m *warp.WMsg, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(c.clientCtx, timeout)
	defer cancel()
	return c.conn.Send(ctx, m)
}

func (c *rpcClient) Call(ctx context.Context, method string, payload json.RawMessage, timeoutMls int64) (*warp.WMsg, error) {
	if method == "" {
		return nil, fmt.Errorf("missing rpc method")
	}
	if payload == nil {
		return nil, fmt.Errorf("missing payload")
	}

	requestId := c.allocateRequestId()

	req := warp.WMsg{Type: "rpc", Payload: payload}
	req.RPC = &warp.RPCMetadata{
		Op:      warp.CallOpcode,
		Method:  method,
		Seq:     requestId,
		Timeout: timeoutMls,
	}

	requestCtx := c.addRequestCtx(requestId, &req, 1)

	err := c.conn.Send(ctx, &req)
	if err != nil {
		requestCtx.Close()
		return nil, fmt.Errorf("wire msg: %w", err)
	}

	res, err := requestCtx.SingleResp(timeoutMls, func() {
		c.CancelRequest(context.Background(), requestId)
	})
	if err != nil {
		requestCtx.Close()
		return nil, err
	}

	return res, err
}

func (c *rpcClient) Subscribe(ctx context.Context, method string, payload json.RawMessage, receiveCap int, firstTimeoutMls int64) (*warp.WMsg, <-chan *warp.WMsg, int64, error) {
	if method == "" {
		return nil, nil, 0, fmt.Errorf("missing rpc method")
	}
	if payload == nil {
		return nil, nil, 0, fmt.Errorf("missing payload")
	}

	requestId := c.allocateRequestId()

	req := warp.WMsg{Type: "rpc", Payload: payload}
	req.RPC = &warp.RPCMetadata{
		Op:      warp.GetStreamOpcode,
		Method:  method,
		Seq:     requestId,
		Timeout: firstTimeoutMls,
	}

	requestCtx := c.addRequestCtx(requestId, &req, receiveCap)

	err := c.conn.Send(ctx, &req)
	if err != nil {
		requestCtx.Close()
		return nil, nil, 0, fmt.Errorf("wire msg: %w", err)
	}

	firstResponse, err := requestCtx.SingleResp(firstTimeoutMls, func() {
		c.CancelRequest(context.Background(), requestCtx.requestId)
	})
	if err != nil {
		requestCtx.Close()
		return nil, nil, 0, err
	}

	return firstResponse, requestCtx.MultiResp(), requestCtx.requestId, err
}

func (c *rpcClient) CancelRequest(ctx context.Context, requestId int64) {
	_ = c.conn.Send(ctx, &warp.WMsg{
		Type: warp.RPCType, RPC: &warp.RPCMetadata{
			Op:  warp.CancelOpcode,
			Seq: requestId,
		},
	})
}

func (c *rpcClient) getRPCHandler() warp.ResponseHandler {
	return func(resp *warp.WMsg) bool {

		if resp.Type != warp.RPCType {
			return false
		}

		if resp.RPC == nil {
			// missing RPC metadata
			fmt.Printf("rpc: missing RPC metadata\n")
			return false
		}

		entry, ok := c.requestCtxMap.Load(resp.RPC.Seq)
		if !ok {
			fmt.Printf("rpc: incorrect RPC seq number\n")
			return false
		}

		requestCtx := entry.(*rpcRequestCtx)

		switch resp.RPC.Op {
		case warp.ResultOpcode:
			requestCtx.notifyResult(resp)
			c.sendMetrics(requestCtx)
			requestCtx.Close()
			c.requestCtxMap.Delete(requestCtx.requestId)
			return true

		case warp.ErrorOpcode:
			serverErr := fmt.Errorf("%d - %s", resp.RPC.ErrorCode, resp.RPC.ErrorMessage)
			requestCtx.SetError(serverErr)
			requestCtx.Close()
			c.requestCtxMap.Delete(requestCtx.requestId)
			return true

		case warp.StartOpcode:
			requestCtx.notifyResult(resp)
			c.sendMetrics(requestCtx)
			return true

		case warp.ValueOpcode:
			requestCtx.notifyResult(resp)
			return true

		case warp.EndOpcode:
			requestCtx.notifyResult(resp)
			if requestCtx.TryGetClose() {
				c.requestCtxMap.Delete(requestCtx.requestId)
			}
			return true

		case warp.CancelOpcode:
			if requestCtx.TryPutClose() {
				c.requestCtxMap.Delete(requestCtx.requestId)
			}
			return true

		default:
			return false
		}

	}
}

func (t *rpcClient) SetMonitor(perfMonitor warp.PerformanceMonitor) {
	t.perfMonitor.Store(perfMonitor)
}

func (t *rpcClient) sendMetrics(requestCtx *rpcRequestCtx) {
	mon := t.perfMonitor.Load()
	if mon != nil {
		mon.(warp.PerformanceMonitor)(requestCtx.Type(), requestCtx.Elapsed())
	}
}
