package wserver

import (
	"fmt"
	"morpheus-poc/warp"
	"sync"

	"go.uber.org/atomic"
)

type rpcServer struct {
	clientMap   sync.Map // key is clientId, value *servingClient
	functionMap sync.Map // key is function name, value *function
	nextClient  atomic.Int64
	closed      atomic.Bool
}

func NewRPCServer() warp.Server {
	return &rpcServer{}
}

func (t *rpcServer) Close() error {
	if !t.closed.CAS(false, true) {
		return nil
	}
	t.clientMap.Range(func(k, v any) bool {
		if cli, ok := v.(*servingClient); ok {
			cli.Close()
		}
		t.clientMap.Delete(k)
		return true
	})
	return nil
}

func (t *rpcServer) Attach(conn warp.Connection) {
	id := conn.SessionID()
	t.clientMap.Store(id, newServingClient(id, conn, &t.functionMap))
}

func (t *rpcServer) Detach(conn warp.Connection) {
	id := conn.SessionID()
	v, ok := t.clientMap.Load(id)
	if !ok {
		return
	}
	t.clientMap.Delete(id)
	if cli, ok := v.(*servingClient); ok {
		cli.Close()
	}
}

func (t *rpcServer) Serve(conn warp.Connection, msg warp.WMsg) error {
	id := conn.SessionID()
	if t.closed.Load() {
		return fmt.Errorf("rpc wserver closed")
	}
	v, ok := t.clientMap.Load(id)
	if !ok {
		return fmt.Errorf("rpc wclient %d not attached", id)
	}
	cli := v.(*servingClient)
	return cli.ServeMessage(msg)
}
