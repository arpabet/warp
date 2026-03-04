package wio

import (
	"morpheus-poc/warp"

	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"
)

type msgpackSerializer struct{}

func NewMsgPackSerializer() warp.Serializer { return &msgpackSerializer{} }

func (m *msgpackSerializer) MessageType() int { return websocket.BinaryMessage }

func (m *msgpackSerializer) ContentType() string { return "application/x-msgpack" }

func (m *msgpackSerializer) Marshal(v any) ([]byte, error) {
	return msgpack.Marshal(v)
}

func (m *msgpackSerializer) Unmarshal(data []byte, v any) error {
	return msgpack.Unmarshal(data, v)
}
