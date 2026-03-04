package wserver

import (
	"context"
	"fmt"
	"morpheus-poc/warp"
	"sync"
	"time"
)

var OutgoingQueueCap = 4096

type servingClient struct {
	sessionID   int64
	conn        warp.Connection
	functionMap *sync.Map

	requestMap       sync.Map // seq -> *servingRequest
	canceledRequests sync.Map // seq -> struct{}

	writeMu   sync.Mutex
	closeOnce sync.Once
}

func newServingClient(sessionID int64, conn warp.Connection, functionMap *sync.Map) *servingClient {
	return &servingClient{
		sessionID:   sessionID,
		conn:        conn,
		functionMap: functionMap,
	}
}

func (c *servingClient) Close() {
	c.closeOnce.Do(func() {
		c.requestMap.Range(func(_, v any) bool {
			if sr, ok := v.(*servingRequest); ok {
				sr.Close()
			}
			return true
		})
	})
}

func functionResult(seq int64, payload []byte) *warp.WMsg {
	return &warp.WMsg{
		Type:    warp.RPCType,
		RPC:     &warp.RPCMetadata{Op: warp.ResultOpcode, Seq: seq},
		Payload: payload,
	}
}

func functionError(seq int64, code int64, format string, args ...any) *warp.WMsg {
	return &warp.WMsg{
		Type: warp.RPCType,
		RPC: &warp.RPCMetadata{
			Op:           warp.ErrorOpcode,
			Seq:          seq,
			ErrorCode:    code,
			ErrorMessage: fmt.Sprintf(format, args...),
		},
	}
}

func streamStart(seq int64, method string, payload []byte) *warp.WMsg {
	return &warp.WMsg{
		Type:    warp.RPCType,
		RPC:     &warp.RPCMetadata{Op: warp.StartOpcode, Seq: seq, Method: method},
		Payload: payload,
	}
}

func streamValue(seq int64, method string, payload []byte) *warp.WMsg {
	return &warp.WMsg{
		Type:    warp.RPCType,
		RPC:     &warp.RPCMetadata{Op: warp.ValueOpcode, Seq: seq, Method: method},
		Payload: payload,
	}
}

func streamEnd(seq int64, method string, payload []byte) *warp.WMsg {
	return &warp.WMsg{
		Type:    warp.RPCType,
		RPC:     &warp.RPCMetadata{Op: warp.EndOpcode, Seq: seq, Method: method},
		Payload: payload,
	}
}

type rpcCodedError interface {
	RPCCode() int64
}

func codeFromErr(err error, fallback int64) int64 {
	if err == nil {
		return fallback
	}
	if ce, ok := err.(rpcCodedError); ok {
		return ce.RPCCode()
	}
	return fallback
}

func (c *servingClient) send(msg *warp.WMsg) error {
	if msg == nil {
		return nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.conn.Send(ctx, msg)
}

func (c *servingClient) findFunction(name string) (*function, bool) {
	if fn, ok := c.functionMap.Load(name); ok {
		return fn.(*function), true
	}
	return nil, false
}

// ServeMessage processes one inbound RPC frame.
func (c *servingClient) ServeMessage(msg warp.WMsg) error {

	seq := msg.RPC.Seq
	if _, canceled := c.canceledRequests.Load(seq); canceled {
		c.canceledRequests.Delete(seq)
		return c.send(functionError(seq, 499, "request canceled"))
	}

	switch msg.RPC.Op {
	case warp.CancelOpcode:
		c.canceledRequests.Store(seq, struct{}{})
		if sr, ok := c.findServingRequest(seq); ok {
			_ = sr.closeRequest(c)
		}
		return nil
	case warp.CallOpcode:
		return c.serveFunctionRequest(singleFunction, msg)
	case warp.GetStreamOpcode:
		return c.serveFunctionRequest(outgoingStream, msg)
	case warp.PutStreamOpcode:
		return c.serveIncomingFrame(msg)
	case warp.BiStreamOpcode:
		return c.serveBiDirectionalFrame(msg)
	default:
		return c.send(functionError(seq, 400, "unknown rpc op %q", msg.RPC.Op))
	}
}

func (c *servingClient) serveFunctionRequest(ft functionType, req warp.WMsg) error {
	resp := c.doServeFunctionRequest(ft, req)
	if resp != nil {
		return c.send(resp)
	}
	return nil
}

func (c *servingClient) doServeFunctionRequest(ft functionType, req warp.WMsg) *warp.WMsg {
	if req.RPC == nil {
		return functionError(0, 400, "missing rpc metadata")
	}
	seq := req.RPC.Seq
	name := req.RPC.Method
	if name == "" {
		return functionError(seq, 400, "missing method")
	}

	fn, ok := c.findFunction(name)
	if !ok {
		return functionError(seq, 404, "function not found %s", name)
	}
	if fn.ft != ft {
		return functionError(seq, 400, "function %s has different type", name)
	}

	switch fn.ft {
	case singleFunction:
		res, err := fn.singleFn(c.conn, req)
		if err != nil {
			return functionError(seq, codeFromErr(err, 500), "single function %s call: %v", name, err)
		}
		b, err := c.conn.Serializer().Marshal(res)
		if err != nil {
			return functionError(seq, 500, "single function %s marshal: %v", name, err)
		}
		return functionResult(seq, b)

	case outgoingStream:
		sr := c.newServingRequest(ft, seq)
		outC, err := fn.outStream(c.conn, req)
		if err != nil {
			_ = sr.closeRequest(c)
			return functionError(seq, 500, "out stream %s call: %v", name, err)
		}
		go sr.outgoingStreamer(name, outC, c)
		return nil

	case incomingStream:
		sr := c.newServingRequest(ft, seq)
		if err := sr.pushFrame(req); err != nil {
			_ = sr.closeRequest(c)
			return functionError(seq, 400, "incoming stream %s push: %v", name, err)
		}
		go func() {
			err := fn.inStream(c.conn, req, sr.inC)
			if err != nil {
				_ = c.send(functionError(seq, 500, "in stream %s call: %v", name, err))
			}
			_ = sr.closeRequest(c)
		}()
		ack, err := c.conn.Serializer().Marshal(map[string]any{"accepted": true})
		if err != nil {
			return functionError(seq, 500, "incoming stream ack marshal: %v", err)
		}
		return streamStart(seq, name, ack)

	case biStream:
		sr := c.newServingRequest(ft, seq)
		if err := sr.pushFrame(req); err != nil {
			_ = sr.closeRequest(c)
			return functionError(seq, 400, "biStream %s push: %v", name, err)
		}
		outC, err := fn.chat(c.conn, req, sr.inC)
		if err != nil {
			_ = sr.closeRequest(c)
			return functionError(seq, 500, "biStream %s call: %v", name, err)
		}
		go sr.outgoingStreamer(name, outC, c)
		return nil

	default:
		return functionError(seq, 400, "unsupported function type for op")
	}
}

func (c *servingClient) serveIncomingFrame(req warp.WMsg) error {
	if req.RPC == nil {
		return nil
	}
	seq := req.RPC.Seq
	if existing, ok := c.findServingRequest(seq); ok {
		return existing.pushFrame(req)
	}
	return c.serveFunctionRequest(incomingStream, req)
}

func (c *servingClient) serveBiDirectionalFrame(req warp.WMsg) error {
	if req.RPC == nil {
		return nil
	}
	seq := req.RPC.Seq
	if existing, ok := c.findServingRequest(seq); ok {
		return existing.pushFrame(req)
	}
	return c.serveFunctionRequest(biStream, req)
}

func (c *servingClient) newServingRequest(ft functionType, reqID int64) *servingRequest {
	sr := newServingRequest(ft, reqID)
	c.requestMap.Store(reqID, sr)
	return sr
}

func (c *servingClient) findServingRequest(reqID int64) (*servingRequest, bool) {
	v, ok := c.requestMap.Load(reqID)
	if !ok {
		return nil, false
	}
	sr, ok := v.(*servingRequest)
	return sr, ok
}

func (c *servingClient) deleteRequest(reqID int64) {
	c.requestMap.Delete(reqID)
}
