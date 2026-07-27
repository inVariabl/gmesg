package ipc

import "encoding/json"

const SocketName = "gmesg.sock"

type Request struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type Response struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type Event struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}
