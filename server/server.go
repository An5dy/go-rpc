package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/An5dy/go-rpc/codec"
)

// MagicNumber 序列化方式
const MagicNumber = 0x3bef5c

// Option 请求协议信息
// eg：
//		| Option{MagicNumber: xxx, CodecType: xxx} | Header{ServiceMethod ...} | Body interface{} |
// 		| <------      固定 JSON 编码      ------>  | <-------   编码方式由 CodeType 决定   ------->|
//
// 在一次连接中，Option 固定在报文的最开始，Header 和 Body 可以有多个，即报文可能是这样的：
//		| Option | Header1 | Body1 | Header2 | Body2 | ...
type Option struct {
	MagicNumber    int           // 请求类型
	CodecType      codec.Type    // 编码类型
	ConnectTimeout time.Duration // 连接超时
	HandleTimeout  time.Duration // 处理超时
}

// DefaultOption 默认协议
var DefaultOption = &Option{
	MagicNumber:    MagicNumber,
	CodecType:      codec.GobType,
	ConnectTimeout: time.Second * 10, // 默认超时 10 秒
}

// Server RPC 服务器
type Server struct {
	serviceMap sync.Map
}

// NewServer Server 实例构造函数
func NewServer() *Server {
	return &Server{}
}

// DefaultServer 默认服务器
var DefaultServer = NewServer()

// Accept 监听并接收请求
func (server *Server) Accept(lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			log.Panicln("rpc server: accept error:", err)
			return
		}
		// 子协程处理监听到的请求连接
		go server.ServeConn(conn)
	}
}

func Accept(lis net.Listener) { DefaultServer.Accept(lis) }

// ServeConn 处理连接
func (server *Server) ServeConn(conn io.ReadWriteCloser) {
	// 延迟关闭连接
	defer func() {
		_ = conn.Close()
	}()
	// 1.	解析 Option
	var opt Option
	if err := json.NewDecoder(conn).Decode(&opt); err != nil {
		log.Println("rpc server: options error: ", err)
		return
	}
	// 2. 判断当前请求是不是一个 rpc 请求
	if opt.MagicNumber != MagicNumber {
		log.Printf("rpc server: invalid magic number %x", opt.MagicNumber)
		return
	}
	// 3. 解析请求体
	f := codec.NewCodecFuncMap[opt.CodecType]
	if f == nil {
		log.Printf("rpc server: invalid codec type %s", opt.CodecType)
		return
	}
	server.serveCodec(f(conn), &opt)
}

// invalidRequest 错误响应 argv 占位符
var invalidRequest = struct{}{}

// serveCodec 解析请求数据
func (server *Server) serveCodec(cc codec.Codec, opt *Option) {
	sending := new(sync.Mutex) // 确保一次完整的响应数据
	wg := new(sync.WaitGroup)  // 处理完所有请求，没处理完一直等待
	for {
		req, err := server.readRequest(cc)
		if err != nil {
			if req == nil {
				break // 无法恢复的错误
			}
			// 设置响应 header 错误信息
			req.h.Error = err.Error()
			// 发送错误响应
			server.sendResponse(cc, req.h, invalidRequest, sending)
			continue
		}
		wg.Add(1)
		// 处理请求
		go server.handleRequest(cc, req, sending, wg, opt.HandleTimeout)
	}
	wg.Wait()
	_ = cc.Close()
}

// request 请求信息
type request struct {
	h            *codec.Header // 请求头
	argv, replyv reflect.Value // 请求值和响应值
	mtype        *methodType
	svc          *service
}

// readRequestHeader 读取请求头信息
func (server *Server) readRequestHeader(cc codec.Codec) (*codec.Header, error) {
	var h codec.Header
	if err := cc.ReadHeader(&h); err != nil {
		// err 不属于数据读完产生的错误
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			log.Println("rpc server: read header error:", err)
		}
		return nil, err
	}
	return &h, nil
}

// readRequest 读取请求信息
func (server *Server) readRequest(cc codec.Codec) (*request, error) {
	// 1. 获取 header 信息
	h, err := server.readRequestHeader(cc)
	if err != nil {
		return nil, err
	}

	// 2. 设置 request
	req := &request{h: h}
	req.svc, req.mtype, err = server.findService(h.ServiceMethod)
	if err != nil {
		return req, err
	}
	// 创建入参实例
	req.argv = req.mtype.newArgv()
	req.replyv = req.mtype.newReplyv()

	// 3. 读取请求 body
	argvi := req.argv.Interface()
	// 如果 argv 不是指针类型，则获取该值的地址指针
	if req.argv.Type().Kind() != reflect.Ptr {
		argvi = req.argv.Addr().Interface()
	}
	if err = cc.ReadBody(argvi); err != nil {
		log.Println("rpc server: read argv err:", err)
		return req, err
	}
	return req, nil
}

// sendResponse 发送响应
func (server *Server) sendResponse(cc codec.Codec, h *codec.Header, body interface{}, sending *sync.Mutex) {
	// 加互斥锁，确保当次响应能完整发送
	sending.Lock()
	defer sending.Unlock()
	if err := cc.Write(h, body); err != nil {
		log.Println("rpc server: write response error:", err)
	}
}

// handleRequest 处理请求
// 支持超时处理
func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup, timeout time.Duration) {
	defer wg.Done()
	// called 信道接收到消息，代表处理没有超时，继续执行 sendResponse
	called := make(chan struct{})
	sent := make(chan struct{})

	go func() {
		err := req.svc.call(req.mtype, req.argv, req.replyv)
		called <- struct{}{}
		if err != nil {
			req.h.Error = err.Error()
			server.sendResponse(cc, req.h, invalidRequest, sending)
			sent <- struct{}{}
			return
		}
		server.sendResponse(cc, req.h, req.replyv.Interface(), sending)
		sent <- struct{}{}
	}()

	if timeout == 0 {
		<-called
		<-sent
		return
	}

	// 设置处理超时
	select {
	case <-time.After(timeout):
		req.h.Error = fmt.Sprintf("rpc server: request handle timeout: expect within %s", timeout)
		server.sendResponse(cc, req.h, invalidRequest, sending)
	case <-called:
		<-sent
	}
}

// Register 注册服务
func (server *Server) Register(rcvr interface{}) error {
	s := newService(rcvr)
	if _, dup := server.serviceMap.LoadOrStore(s.name, s); dup {
		return errors.New("rpc: service already defined: " + s.name)
	}
	return nil
}

func Register(rcvr interface{}) error { return DefaultServer.Register(rcvr) }

// findService 查找注册的服务
func (server *Server) findService(serviceMethod string) (svc *service, mtype *methodType, err error) {
	dot := strings.LastIndex(serviceMethod, ".")
	if dot < 0 {
		err = errors.New("rpc server: service/method request ill-formed: " + serviceMethod)
		return
	}
	serviceName, methodName := serviceMethod[:dot], serviceMethod[dot+1:]
	svci, ok := server.serviceMap.Load(serviceName)
	if !ok {
		err = errors.New("rpc server: can't find service " + serviceName)
		return
	}
	svc = svci.(*service)
	mtype = svc.method[methodName]
	if mtype == nil {
		err = errors.New("rpc server: can't find method " + methodName)
	}
	return
}
