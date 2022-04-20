package client

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/An5dy/go-rpc/server"
)

// NewHTTPClient
func NewHTTPClient(conn net.Conn, opt *server.Option) (*Client, error) {
	_, _ = io.WriteString(conn, fmt.Sprintf("CONNECT %s HTTP/1.0\n\n", server.DefaultRPCPath))

	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "CONNECT"})
	if err == nil && resp.Status == server.Connected {
		return NewClient(conn, opt)
	}
	if err == nil {
		err = errors.New("unexpected HTTP response: " + resp.Status)
	}
	return nil, err
}

// DialHTTP
func DialHTTP(network, address string, opts ...*server.Option) (*Client, error) {
	return dialTimeout(NewHTTPClient, network, address, opts...)
}
