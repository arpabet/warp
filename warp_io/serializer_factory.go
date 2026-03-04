package warp_io

import (
	"github.com/arpabet/warp"

	"github.com/gorilla/websocket"
)

func TryCreateSerializerFor(messageType int) (warp.Serializer, bool) {
	switch messageType {
	case websocket.TextMessage:
		return NewJSONSerializer(), true
	case websocket.BinaryMessage:
		return NewMsgPackSerializer(), true
	default:
		return nil, false
	}
}
