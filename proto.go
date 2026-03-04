package warp

import (
	"encoding/json"
)

const RPCType = "rpc"

// --- Types shared with wclient (legacy lookup + presence + WS) ---

type WMsg struct {
	Type    string          `json:"tp" msgpack:"tp"`                         // "rpc"
	From    string          `json:"from,omitempty" msgpack:"from,omitempty"` // empty for RPC
	To      string          `json:"to,omitempty" msgpack:"to,omitempty"`     // empty for RPC
	RPC     *RPCMetadata    `json:"rpc,omitempty" msgpack:"rpc,omitempty"`   // not empty for RPC
	Payload json.RawMessage `json:"payload,omitempty" msgpack:"payload,omitempty"`
}

const CallOpcode = "call"
const ResultOpcode = "res"
const ErrorOpcode = "err"
const CancelOpcode = "cancel"
const GetStreamOpcode = "getstream"
const PutStreamOpcode = "putstream"
const BiStreamOpcode = "bistream"
const StartOpcode = "start"
const ValueOpcode = "value"
const EndOpcode = "end"

// RPCMetadata is the communication metadata with the wserver only
type RPCMetadata struct {
	Op           string `json:"op" msgpack:"op"` // "call", "res", "err", "cancel", "getstream", "putstream", "bistream", "start", "value", "end"
	Method       string `json:"method" msgpack:"method"`
	Seq          int64  `json:"seq,omitempty" msgpack:"seq,omitempty"`                     // IN
	Timeout      int64  `json:"timeout,omitempty" msgpack:"timeout,omitempty"`             // IN
	ErrorCode    int64  `json:"error,omitempty" msgpack:"error,omitempty"`                 // OUT  HTTP like error code
	ErrorMessage string `json:"error_message,omitempty" msgpack:"error_message,omitempty"` // OUT  HTTP like error message
}
