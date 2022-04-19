package codec

import "io"

// Header rpc 请求头
type Header struct {
	// ServiceMethod 是服务名和方法名，通常与 Go 语言中的结构体和方法相映射。
	//		format "Service.Method"
	ServiceMethod string
	// Seq 是请求的序号，也可以认为是某个请求的 ID，用来区分不同的请求。
	Seq uint64
	// Error 是错误信息，客户端置为空，服务端如果如果发生错误，将错误信息置于 Error 中。
	Error string
}

// Codec 对消息体进行编解码的接口
type Codec interface {
	io.Closer                         // 关闭连接
	ReadHeader(*Header) error         // 读取 Header
	ReadBody(interface{}) error       // 读取 Body
	Write(*Header, interface{}) error // 写入数据
}

// NewCodecFunc 解析器构造函数类型
type NewCodecFunc func(io.ReadWriteCloser) Codec

// 数据类型
type Type string

const (
	GobType  Type = "application/gob"
	JsonType Type = "application/json"
)

// NewCodecFuncMap 支持的解析器
var NewCodecFuncMap map[Type]NewCodecFunc

func init() {
	NewCodecFuncMap = make(map[Type]NewCodecFunc)
	NewCodecFuncMap[GobType] = NewGobCodec
}
