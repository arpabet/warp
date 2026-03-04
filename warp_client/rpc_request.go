/*
 * Copyright (c) 2025 Karagatan LLC.
 * SPDX-License-Identifier: BUSL-1.1
 */

package warp_client

import (
	"github.com/arpabet/warp"
	"log"
	"time"

	"go.uber.org/atomic"
)

const getStreamFlag = 1
const putStreamFlag = 2

type rpcRequestCtx struct {
	requestId int64
	state     atomic.Int32
	req       *warp.WMsg
	start     time.Time
	resultCh  chan *warp.WMsg
	resultErr atomic.Error
}

func newRequestCtx(requestId int64, req *warp.WMsg, receiveCap int) *rpcRequestCtx {
	t := &rpcRequestCtx{
		requestId: requestId,
		req:       req,
		start:     time.Now(),
		resultCh:  make(chan *warp.WMsg, receiveCap),
	}
	t.state.Store(getStreamFlag + putStreamFlag)
	return t
}

func (t *rpcRequestCtx) Type() string {
	return t.req.Type
}

func (t *rpcRequestCtx) Stats() (int, int) {
	return len(t.resultCh), cap(t.resultCh)
}

func (t *rpcRequestCtx) Elapsed() int64 {
	elapsed := time.Now().Sub(t.start)
	return elapsed.Microseconds()
}

func (t *rpcRequestCtx) notifyResult(res *warp.WMsg) {
	if !t.IsGetOpen() {
		return
	}
	select {
	case t.resultCh <- res:
	default:
		if res.RPC != nil {
			log.Printf("rpcRequestCtx: result channel full for type=%q op=%s seq=%d\n", res.Type, res.RPC.Op, res.RPC.Seq)
		} else {
			log.Printf("rpcRequestCtx: result channel full for type=%q empty RPC\n", res.Type)
		}
		t.TryGetClose()
	}
}

func (t *rpcRequestCtx) Close() {
	doClose := false

	for {
		st := t.state.Load()
		if st&getStreamFlag > 0 {
			if t.state.CompareAndSwap(st, 0) {
				doClose = true
				break
			}
		} else {
			break
		}
	}

	if doClose {
		close(t.resultCh)
	}

}

func (t *rpcRequestCtx) IsGetOpen() bool {
	st := t.state.Load()
	return st&getStreamFlag > 0
}

func (t *rpcRequestCtx) TryGetClose() bool {

	closed := false
	for {
		st := t.state.Load()
		if st&getStreamFlag > 0 {
			if t.state.CompareAndSwap(st, st-getStreamFlag) {
				close(t.resultCh)
				closed = true
				break
			}
		} else {
			closed = true
			break
		}
	}

	return closed
}

func (t *rpcRequestCtx) IsPutOpen() bool {
	st := t.state.Load()
	return st&putStreamFlag > 0
}

func (t *rpcRequestCtx) TryPutClose() bool {
	// Just clear the put flag. DO NOT close resultCh here.
	closed := false
	for {
		st := t.state.Load()
		if st&putStreamFlag > 0 {
			if t.state.CompareAndSwap(st, st-putStreamFlag) {
				closed = true
				break
			}
		} else {
			closed = true
			break
		}
	}
	return closed
}

func (t *rpcRequestCtx) SetError(err error) {
	t.resultErr.Store(err)
}

func (t *rpcRequestCtx) Error(defaultError error) error {
	e := t.resultErr.Load()
	if e != nil {
		return e
	}
	return defaultError
}

func (t *rpcRequestCtx) SingleResp(timeoutMls int64, onTimeout func()) (*warp.WMsg, error) {
	select {
	case result, ok := <-t.resultCh:
		if !ok {
			return nil, t.Error(ErrNoResponse)
		}
		return result, nil
	case <-time.After(time.Duration(timeoutMls) * time.Millisecond):
		onTimeout()
		return nil, t.Error(ErrTimeoutError)
	}
}

func (t *rpcRequestCtx) MultiResp() <-chan *warp.WMsg {
	return t.resultCh
}
