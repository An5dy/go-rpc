package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/An5dy/go-rpc/codec"
	"github.com/An5dy/go-rpc/server"
)

// Call 调用实例
type Call struct {
	Seq           uint64      // 请求编号
	ServiceMethod string      // format "Service.Method"
	Args          interface{} // 方法参数
	Reply         interface{} // 方法返回
	Error         error       // 错误
	Done          chan *Call  // 调用结束后执行，通知调用方
}

// done 异步通知调用方
func (call *Call) done() {
	call.Done <- call
}

// 调用客户端
type Client struct {
	// cc 是消息的编解码器，和服务端类似，用来序列化将要发送出去的请求，以及反序列化接收到的响应
	cc codec.Codec
	// opt 服务器自定义协议选项
	opt *server.Option
	// sending 是一个互斥锁，和服务端类似，为了保证请求的有序发送，即防止出现多个请求报文混淆。
	sending sync.Mutex
	// header 是每个请求的消息头，header 只有在请求发送时才需要，
	// 而请求发送是互斥的，因此每个客户端只需要一个，声明在 Client 结构体中可以复用。
	header codec.Header
	// mu 确认所有的请求都能被处理
	mu sync.Mutex
	// seq 用于给发送的请求编号，每个请求拥有唯一编号。
	seq uint64
	// pending 存储未处理完的请求，键是编号，值是 Call 实例。
	pending map[uint64]*Call
	// 客户端关闭，调用方主动关闭设置 true
	closing bool
	// 客户端关闭，发生错误时设置 true
	shutdown bool
}

// Client 实现 io.Closer 的 Close 方法
var _ io.Closer = (*Client)(nil)

// 客户端关闭错误
var ErrShutdown = errors.New("connection is shut down")

// Close 关闭连接
func (client *Client) Close() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closing {
		return ErrShutdown
	}
	client.closing = true
	return client.cc.Close()
}

// IsAvailable 客户端是否可用
func (client *Client) IsAvailable() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return !client.closing && !client.shutdown
}

// registerCall 将参数 call 添加到 client.pending 中，并更新 client.seq。
func (client *Client) registerCall(call *Call) (uint64, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closing || client.shutdown {
		return 0, ErrShutdown
	}
	call.Seq = client.seq
	client.pending[call.Seq] = call
	client.seq++
	return call.Seq, nil
}

// removeCall 根据 seq，从 client.pending 中移除对应的 call，并返回。
func (client *Client) removeCall(seq uint64) *Call {
	client.mu.Lock()
	defer client.mu.Unlock()
	call := client.pending[seq]
	delete(client.pending, seq)
	return call
}

// terminateCalls 服务端或客户端发生错误时调用，将 shutdown 设置为 true，且将错误信息通知所有 pending 状态的 call。
func (client *Client) terminateCalls(err error) {
	// 防止正在处理的请求被意外停止
	client.sending.Lock()
	defer client.sending.Unlock()
	client.mu.Lock()
	defer client.mu.Unlock()
	client.shutdown = true
	for _, call := range client.pending {
		call.Error = err
		call.done()
	}
}

// receive 接收响应
// 接收到的响应有三种情况：
//		call 不存在，可能是请求没有发送完整，或者因为其他原因被取消，但是服务端仍旧处理了。
//		call 存在，但服务端处理出错，即 h.Error 不为空。
// 		call 存在，服务端处理正常，那么需要从 body 中读取 Reply 的值。
func (client *Client) receive() {
	var err error
	for err == nil {
		// 解析 header
		var h codec.Header
		if err = client.cc.ReadHeader(&h); err != nil {
			break
		}
		call := client.removeCall(h.Seq)
		switch {
		case call == nil:
			err = client.cc.ReadBody(nil)
		case h.Error != "":
			call.Error = fmt.Errorf(h.Error)
			err = client.cc.ReadBody(nil)
			call.done()
		default:
			err = client.cc.ReadBody(call.Reply)
			if err != nil {
				call.Error = errors.New("reading body " + err.Error())
			}
			call.done()
		}
	}
	// 接收响应出现错误，则终止所有的 Call
	client.terminateCalls(err)
}

// NewClient Client 构造函数
func NewClient(conn net.Conn, opt *server.Option) (*Client, error) {
	f := codec.NewCodecFuncMap[opt.CodecType]
	if f == nil {
		err := fmt.Errorf("invalid codec type %s", opt.CodecType)
		log.Println("rpc client: codec error:", err)
		return nil, err
	}
	// 发送 Option
	if err := json.NewEncoder(conn).Encode(opt); err != nil {
		log.Println("rpc client: options error: ", err)
		_ = conn.Close()
		return nil, err
	}
	return newClientCodec(f(conn), opt), nil
}

// newClientCodec 构造客户端解析器
func newClientCodec(cc codec.Codec, opt *server.Option) *Client {
	client := &Client{
		seq:     1, // 请求序列号从 1 开始，0 代表 call 不可用
		cc:      cc,
		opt:     opt,
		pending: make(map[uint64]*Call),
	}
	// 开启子协程接收响应
	go client.receive()
	return client
}

// parseOptions 解析 Option
// opts 可以不传，默认为 server.DefaultOption
func parseOptions(opts ...*server.Option) (*server.Option, error) {
	//
	if len(opts) == 0 || opts[0] == nil {
		return server.DefaultOption, nil
	}
	if len(opts) != 1 {
		return nil, errors.New("number of options is more than 1")
	}
	opt := opts[0]
	opt.MagicNumber = server.DefaultOption.MagicNumber
	if opt.CodecType == "" {
		opt.CodecType = server.DefaultOption.CodecType
	}
	return opt, nil
}

// send 发送请求
func (client *Client) send(call *Call) {
	// 确认客户端成功发送完整请求
	client.sending.Lock()
	defer client.sending.Unlock()

	// 注册 call
	seq, err := client.registerCall(call)
	if err != nil {
		call.Error = err
		call.done()
		return
	}

	// 设置请求头
	client.header.ServiceMethod = call.ServiceMethod
	client.header.Seq = seq
	client.header.Error = ""

	// 发送请求数据
	if err := client.cc.Write(&client.header, call.Args); err != nil {
		call := client.removeCall(seq)
		if call != nil {
			call.Error = err
			call.done()
		}
	}
}

// Go 是一个异步接口，返回 call 实例。
func (client *Client) Go(serviceMethod string, args, reply interface{}, done chan *Call) *Call {
	if done == nil {
		done = make(chan *Call, 10)
	} else if cap(done) == 0 {
		log.Panic("rpc client: done channel is unbuffered")
	}
	call := &Call{
		ServiceMethod: serviceMethod,
		Args:          args,
		Reply:         reply,
		Done:          done,
	}
	client.send(call)
	return call
}

// Call 是对 Go 的封装，阻塞 call.Done，等待响应返回，是一个同步接口。
// 使用 context 包实现，控制权交给用户，控制更为灵活
func (client *Client) Call(ctx context.Context, serviceMethod string, args, reply interface{}) error {
	call := client.Go(serviceMethod, args, reply, make(chan *Call, 1))

	select {
	case <-ctx.Done():
		client.removeCall(call.Seq)
		return errors.New("rpc client: call failed: " + ctx.Err().Error())
	case call := <-call.Done:
		return call.Error
	}
}

// clientResult 连接返回结果
type clientResult struct {
	client *Client
	err    error
}

// newClientFunc Client 构造函数类型
type newClientFunc func(conn net.Conn, opt *server.Option) (client *Client, err error)

// dialTimeout 支持超时连接
func dialTimeout(f newClientFunc, network, address string, opts ...*server.Option) (client *Client, err error) {
	opt, err := parseOptions(opts...)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout(network, address, opt.ConnectTimeout)
	if err != nil {
		return nil, err
	}

	// 如果发生错误，关闭连接
	defer func() {
		if err != nil {
			_ = conn.Close()
		}
	}()
	ch := make(chan clientResult)
	go func() {
		client, err := f(conn, opt)
		ch <- clientResult{client: client, err: err}
	}()
	if opt.ConnectTimeout == 0 {
		result := <-ch
		return result.client, result.err
	}
	select {
	case <-time.After(opt.ConnectTimeout):
		return nil, fmt.Errorf("rpc client: connect timeout: expect within %s", opt.ConnectTimeout)
	case result := <-ch:
		return result.client, result.err
	}
}

// Dial 连接服务端
func Dial(network, address string, opts ...*server.Option) (client *Client, err error) {
	return dialTimeout(NewClient, network, address, opts...)
}

// XDial
func XDial(rpcAddr string, opts ...*server.Option) (*Client, error) {
	parts := strings.Split(rpcAddr, "@")
	if len(parts) != 2 {
		return nil, fmt.Errorf("rpc client err: wrong format '%s', expect protocol@addr", rpcAddr)
	}
	protocol, addr := parts[0], parts[1]
	switch protocol {
	case "http":
		return DialHTTP("tcp", addr, opts...)
	default:
		return Dial(protocol, addr, opts...)
	}
}
