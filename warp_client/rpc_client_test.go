package warp_client

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/arpabet/warp"
	"testing"
	"time"
)

type sendFailConn struct {
	sendErr error
}

func (c *sendFailConn) SessionID() int64                { return 1 }
func (c *sendFailConn) SetHandler(h warp.Handler)       {}
func (c *sendFailConn) Serializer() warp.Serializer     { return nil }
func (c *sendFailConn) SetSerializer(s warp.Serializer) {}
func (c *sendFailConn) Close() error                    { return nil }
func (c *sendFailConn) Endpoint() string                { return "test" }
func (c *sendFailConn) Send(ctx context.Context, msg *warp.WMsg) error {
	return c.sendErr
}

func mapLen(m *syncMapAdapter) int {
	n := 0
	m.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}

type syncMapAdapter struct {
	rangeFn func(func(any, any) bool)
}

func (a *syncMapAdapter) Range(fn func(any, any) bool) {
	a.rangeFn(fn)
}

func TestCallDeletesRequestCtxWhenSendFails(t *testing.T) {
	c := &rpcClient{
		clientCtx:    context.Background(),
		writeTimeout: time.Second,
		conn:         &sendFailConn{sendErr: errors.New("send failed")},
	}

	_, err := c.Call(context.Background(), "echo", json.RawMessage(`{"x":1}`), 500)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	count := 0
	c.requestCtxMap.Range(func(_, _ any) bool {
		count++
		return true
	})
	if count != 0 {
		t.Fatalf("expected requestCtxMap to be empty, got %d entries", count)
	}
}

func TestSubscribeDeletesRequestCtxWhenSendFails(t *testing.T) {
	c := &rpcClient{
		clientCtx:    context.Background(),
		writeTimeout: time.Second,
		conn:         &sendFailConn{sendErr: errors.New("send failed")},
	}

	_, _, _, err := c.Subscribe(context.Background(), "stream", json.RawMessage(`{"x":1}`), 1, 500)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	count := 0
	c.requestCtxMap.Range(func(_, _ any) bool {
		count++
		return true
	})
	if count != 0 {
		t.Fatalf("expected requestCtxMap to be empty, got %d entries", count)
	}
}
