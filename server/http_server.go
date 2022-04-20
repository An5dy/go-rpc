package server

import (
	"io"
	"log"
	"net/http"
)

const (
	Connected        = "200 Connected to Gee RPC"
	DefaultRPCPath   = "/_gorpc_"
	DefaultDebugPath = "/debug/gorpc"
)

func (server *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != "CONNECT" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = io.WriteString(w, "405 must CONNECT\n")
		return
	}
	// 透传 http 请求
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		log.Print("rpc hijacking ", req.RemoteAddr, ": ", err.Error())
		return
	}

	_, _ = io.WriteString(conn, "HTTP/1.0 "+Connected+"\n\n")
	server.ServeConn(conn)
}

// 处理 http 请求
func (server *Server) HandleHTTP() {
	http.Handle(DefaultRPCPath, server)
	http.Handle(DefaultDebugPath, debugHTTP{server})
	log.Println("rpc server debug path:", DefaultDebugPath)
}

func HandleHTTP() {
	DefaultServer.HandleHTTP()
}
