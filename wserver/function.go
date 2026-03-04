package wserver

import (
	"morpheus-poc/warp"
)

type functionType int

const (
	singleFunction functionType = iota
	outgoingStream
	incomingStream
	biStream
)

type function struct {
	name      string
	ft        functionType
	singleFn  warp.Function
	outStream warp.OutgoingStream
	inStream  warp.IncomingStream
	chat      warp.BiStream
}

func (t *rpcServer) RegisterFunction(name string, cb warp.Function) {

	fn := &function{
		name:     name,
		ft:       singleFunction,
		singleFn: cb,
	}

	t.functionMap.Store(name, fn)
}

func (t *rpcServer) RegisterOutgoingStream(name string, cb warp.OutgoingStream) {

	fn := &function{
		name:      name,
		ft:        outgoingStream,
		outStream: cb,
	}

	t.functionMap.Store(name, fn)
}

func (t *rpcServer) RegisterIncomingStream(name string, cb warp.IncomingStream) {

	fn := &function{
		name:     name,
		ft:       incomingStream,
		inStream: cb,
	}

	t.functionMap.Store(name, fn)
}

func (t *rpcServer) RegisterBiStream(name string, cb warp.BiStream) {

	fn := &function{
		name: name,
		ft:   biStream,
		chat: cb,
	}

	t.functionMap.Store(name, fn)
}
