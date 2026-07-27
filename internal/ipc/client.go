package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
)

type Client struct {
	conn      net.Conn
	enc       *json.Encoder
	scanner   *bufio.Scanner
	mu        sync.Mutex
	nextID    int
	pending   map[int]chan Response
	pendingMu sync.Mutex
	Events    chan Event
	done      chan struct{}
	closeOnce sync.Once
}

func Dial(socketPath string) (*Client, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, err
	}
	c := &Client{
		conn:    conn,
		enc:     json.NewEncoder(conn),
		scanner: bufio.NewScanner(conn),
		pending: make(map[int]chan Response),
		Events:  make(chan Event, 128),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *Client) readLoop() {
	defer close(c.done)
	for c.scanner.Scan() {
		var msg json.RawMessage
		if err := json.Unmarshal(c.scanner.Bytes(), &msg); err != nil {
			continue
		}
		var evt Event
		if json.Unmarshal(msg, &evt) == nil && evt.Event != "" {
			select {
			case c.Events <- evt:
			default:
			}
			continue
		}
		var resp Response
		if json.Unmarshal(msg, &resp) == nil {
			c.pendingMu.Lock()
			ch, ok := c.pending[resp.ID]
			c.pendingMu.Unlock()
			if ok {
				ch <- resp
			}
		}
	}
}

func (c *Client) nextRequestID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	return c.nextID
}

func (c *Client) Call(method string, params, result any) error {
	id := c.nextRequestID()

	var paramsBytes json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal params: %w", err)
		}
		paramsBytes = b
	}

	ch := make(chan Response, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	req := Request{ID: id, Method: method, Params: paramsBytes}
	func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.enc.Encode(req)
	}()

	resp := <-ch
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	if result != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("unmarshal result: %w", err)
		}
	}
	return nil
}

func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.conn.Close()
	})
}
