package wserver

import (
	"fmt"
	"morpheus-poc/warp"
	"sync"

	"go.uber.org/atomic"
)

var IncomingQueueCap = 4096

type servingRequest struct {
	ft        functionType
	requestID int64
	inC       chan warp.WMsg

	closed atomic.Bool
	once   sync.Once
}

func newServingRequest(ft functionType, requestID int64) *servingRequest {
	sr := &servingRequest{
		ft:        ft,
		requestID: requestID,
	}
	if ft == incomingStream || ft == biStream {
		sr.inC = make(chan warp.WMsg, IncomingQueueCap)
	}
	return sr
}

func (r *servingRequest) Close() {
	r.once.Do(func() {
		r.closed.Store(true)
		if r.inC != nil {
			close(r.inC)
		}
	})
}

func (r *servingRequest) pushFrame(req warp.WMsg) error {
	if req.RPC == nil {
		return nil
	}
	switch req.RPC.Op {
	case warp.CancelOpcode:
		return nil
	case warp.PutStreamOpcode, warp.BiStreamOpcode:
		if r.inC == nil {
			return fmt.Errorf("request %d has no incoming channel", r.requestID)
		}
		if r.closed.Load() {
			return nil
		}
		r.inC <- req
		return nil
	default:
		return fmt.Errorf("unsupported stream op %q for request %d", req.RPC.Op, r.requestID)
	}
}

func (r *servingRequest) closeRequest(cli *servingClient) error {
	cli.deleteRequest(r.requestID)
	r.Close()
	cli.canceledRequests.Delete(r.requestID)
	return nil
}

func (r *servingRequest) outgoingStreamer(method string, outC <-chan warp.WMsg, cli *servingClient) {
	_ = cli.send(streamStart(r.requestID, method, nil))

	for {
		msg, ok := <-outC
		if !ok || r.closed.Load() {
			_ = cli.send(streamEnd(r.requestID, method, nil))
			if r.ft != incomingStream {
				_ = r.closeRequest(cli)
			}
			return
		}
		b, err := cli.conn.Serializer().Marshal(msg)
		if err != nil {
			_ = cli.send(functionError(r.requestID, 500, "stream marshal error: %v", err))
			_ = r.closeRequest(cli)
			return
		}
		if err := cli.send(streamValue(r.requestID, method, b)); err != nil {
			_ = r.closeRequest(cli)
			return
		}
	}
}
