package main

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/An5dy/go-rpc/client"
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
	log.SetFlags(0)
	addr := make(chan string)
	// 1. 启动服务器
	go startServer(addr)

	// 2. 客户端连接服务器
	client, _ := client.Dial("tcp", <-addr)
	defer func() { _ = client.Close() }()

	time.Sleep(time.Second)
	// 发送请求/接收响应
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := fmt.Sprintf("geerpc req %d", i)
			var reply string
			if err := client.Call("Foo.Sum", args, &reply); err != nil {
				log.Fatal("call Foo.Sum error:", err)
			}
			log.Println("reply:", reply)
		}(i)
	}
	wg.Wait()
}
