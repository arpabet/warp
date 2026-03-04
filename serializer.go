package warp

// ─────────────────────────────────────────────────────────────────────────────
// Serialization
// ─────────────────────────────────────────────────────────────────────────────

// Serializer encodes/decodes arbitrary values for on-the-wire exchange.
type Serializer interface {
	// MessageType is the websocket message type
	MessageType() int

	// ContentType returns the MIME-like identifier (e.g., "application/msgpack").
	ContentType() string

	// Marshal encodes v to bytes.
	Marshal(v any) ([]byte, error)

	// Unmarshal decodes data into v (which must be a pointer).
	Unmarshal(data []byte, v any) error
}
