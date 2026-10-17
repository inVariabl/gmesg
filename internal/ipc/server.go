package ipc

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
)

type Handler interface {
	Handle(method string, params json.RawMessage) (json.RawMessage, error)
}

type clientConn struct {
	conn net.Conn
	enc  *json.Encoder
	// wmu serializes writes, since responses and broadcast events are
	// written to the connection from several goroutines.
	wmu sync.Mutex
}

func (cc *clientConn) encode(v any) {
	cc.wmu.Lock()
	defer cc.wmu.Unlock()
	cc.enc.Encode(v)
}

type Server struct {
	socketPath string
	handler    Handler
	listener   net.Listener
	clients    map[*clientConn]struct{}
	mu         sync.Mutex
	done       chan struct{}
}

func NewServer(socketPath string, handler Handler) *Server {
	return &Server{
		socketPath: socketPath,
		handler:    handler,
		clients:    make(map[*clientConn]struct{}),
		done:       make(chan struct{}),
	}
}

func (s *Server) Start() error {
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o700); err != nil {
		return err
	}
	os.Remove(s.socketPath)

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return err
	}
	s.listener = listener

	go s.acceptLoop()
	return nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				continue
			}
		}
		cc := &clientConn{conn: conn, enc: json.NewEncoder(conn)}
		s.mu.Lock()
		s.clients[cc] = struct{}{}
		s.mu.Unlock()
		go s.handleConn(cc)
	}
}

func (s *Server) handleConn(cc *clientConn) {
	defer func() {
		cc.conn.Close()
		s.mu.Lock()
		delete(s.clients, cc)
		s.mu.Unlock()
	}()

	scanner := bufio.NewScanner(cc.conn)
	for scanner.Scan() {
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		// Each request is handled in its own goroutine, so that a request
		// awaiting a slow phone does not delay the requests behind it.
		// Clients match responses to requests by ID, not by order.
		go func(req Request) {
			result, err := s.handler.Handle(req.Method, req.Params)
			resp := Response{ID: req.ID}
			if err != nil {
				resp.Error = err.Error()
			} else if result != nil {
				resp.Result = result
			}
			cc.encode(resp)
		}(req)
	}
}

func (s *Server) Broadcast(event string, data any) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return
	}
	evt := Event{Event: event, Data: dataBytes}
	evtBytes, err := json.Marshal(evt)
	if err != nil {
		return
	}
	line := append(evtBytes, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	for cc := range s.clients {
		cc.wmu.Lock()
		cc.conn.Write(line)
		cc.wmu.Unlock()
	}
}

func (s *Server) Close() {
	close(s.done)
	if s.listener != nil {
		s.listener.Close()
	}
	os.Remove(s.socketPath)
}

func (s *Server) Addr() net.Addr {
	if s.listener != nil {
		return s.listener.Addr()
	}
	return nil
}
