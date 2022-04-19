package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/An5dy/go-rpc/codec"
	"github.com/An5dy/go-rpc/server"
)

// startServer 使用了信道 addr，确保服务端端口监听成功，客户端再发起请求
func startServer(addr chan<- string) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		log.Fatal("network error:", err)
	}
	fmt.Println("start rpc server on", l.Addr())
	addr <- l.Addr().String()
	server.Accept(l)
}

func main() {
	addr := make(chan string)
	// 1. 启动服务器
	go startServer(addr)

	// 2. 客户端连接服务器
	conn, _ := net.Dial("tcp", <-addr)
	defer func() { _ = conn.Close() }()

	time.Sleep(time.Second)
	// 发送 Option
	_ = json.NewEncoder(conn).Encode(server.DefaultOption)
	cc := codec.NewGobCodec(conn)

	for i := 0; i < 5; i++ {
		h := &codec.Header{
			ServiceMethod: "Foo.Sum",
			Seq:           uint64(i),
		}
		_ = cc.Write(h, fmt.Sprintf("geerpc req %d", h.Seq))
		_ = cc.ReadHeader(h)
		var reply string
		_ = cc.ReadBody(&reply)
		log.Println("reply:", reply)
	}
}
