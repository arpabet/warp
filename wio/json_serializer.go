package wio

import (
	"encoding/json"
	"morpheus-poc/warp"

	"github.com/gorilla/websocket"
)

type jsonSerializer struct{}

func NewJSONSerializer() warp.Serializer { return &jsonSerializer{} }

func (j *jsonSerializer) MessageType() int { return websocket.TextMessage }

func (j *jsonSerializer) ContentType() string { return "application/json" }

func (j *jsonSerializer) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (j *jsonSerializer) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
