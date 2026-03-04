package warp_server

import (
	"context"
	"github.com/arpabet/warp"
	"strings"
	"sync"
	"testing"
)

type testConn struct {
	mu   sync.Mutex
	sent []*warp.WMsg
}

func (c *testConn) SessionID() int64                { return 1 }
func (c *testConn) SetHandler(h warp.Handler)       {}
func (c *testConn) Serializer() warp.Serializer     { return nil }
func (c *testConn) SetSerializer(s warp.Serializer) {}
func (c *testConn) Close() error                    { return nil }
func (c *testConn) Endpoint() string                { return "test" }
func (c *testConn) Send(ctx context.Context, msg *warp.WMsg) error {
	c.mu.Lock()
	c.sent = append(c.sent, msg)
	c.mu.Unlock()
	return nil
}

func (c *testConn) sentMessages() []*warp.WMsg {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*warp.WMsg, len(c.sent))
	copy(out, c.sent)
	return out
}

func TestServeMessageWithMissingRPCMetadata(t *testing.T) {
	conn := &testConn{}
	fnMap := &sync.Map{}
	client := newServingClient(1, conn, fnMap)

	err := client.ServeMessage(warp.WMsg{Type: warp.RPCType})
	if err != nil {
		t.Fatalf("ServeMessage returned unexpected error: %v", err)
	}

	sent := conn.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("expected 1 error frame, got %d", len(sent))
	}
	if sent[0] == nil || sent[0].RPC == nil {
		t.Fatalf("expected rpc error frame, got %#v", sent[0])
	}
	if sent[0].RPC.Op != warp.ErrorOpcode {
		t.Fatalf("expected op=%q, got %q", warp.ErrorOpcode, sent[0].RPC.Op)
	}
	if sent[0].RPC.ErrorCode != 400 {
		t.Fatalf("expected error code 400, got %d", sent[0].RPC.ErrorCode)
	}
	if !strings.Contains(sent[0].RPC.ErrorMessage, "missing rpc metadata") {
		t.Fatalf("unexpected error message: %q", sent[0].RPC.ErrorMessage)
	}
}
