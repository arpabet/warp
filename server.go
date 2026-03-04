package warp

type Function func(conn Connection, args WMsg) (WMsg, error)
type OutgoingStream func(conn Connection, args WMsg) (<-chan WMsg, error)
type IncomingStream func(conn Connection, args WMsg, inC <-chan WMsg) error
type BiStream func(conn Connection, args WMsg, inC <-chan WMsg) (<-chan WMsg, error)

type Server interface {
	RegisterFunction(name string, cb Function)
	RegisterOutgoingStream(name string, cb OutgoingStream)
	RegisterIncomingStream(name string, cb IncomingStream)
	RegisterBiStream(name string, cb BiStream)

	Close() error

	Attach(conn Connection)
	Detach(conn Connection)
	Serve(conn Connection, msg WMsg) error
}
