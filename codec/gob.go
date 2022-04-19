package codec

import (
	"bufio"
	"encoding/gob"
	"io"
	"log"
)

// GobCodec Gob 解析器
type GobCodec struct {
	conn io.ReadWriteCloser // 连接
	buf  *bufio.Writer      // 防止阻塞而创建的带缓冲的 Writer，提升性能。
	enc  *gob.Encoder       // 编码
	dec  *gob.Decoder       // 解码
}

var _ Codec = (*GobCodec)(nil)

// NewGobCodec 实例化 Gob 解析器
func NewGobCodec(conn io.ReadWriteCloser) Codec {
	buf := bufio.NewWriter(conn)
	return &GobCodec{
		conn: conn,
		buf:  buf,
		enc:  gob.NewEncoder(buf),
		dec:  gob.NewDecoder(conn),
	}
}

func (c *GobCodec) ReadHeader(h *Header) error {
	return c.dec.Decode(h)
}

func (c *GobCodec) ReadBody(body interface{}) error {
	return c.dec.Decode(body)
}

func (c *GobCodec) Write(h *Header, body interface{}) (err error) {
	defer func() {
		// 获取缓冲中的数据
		_ = c.buf.Flush()
		if err != nil {
			_ = c.Close()
		}
	}()
	// 编码 消息头
	if err := c.enc.Encode(h); err != nil {
		log.Panicln("rpc codec: gob error encoding header:", err)
		return err
	}
	// 编码 消息体
	if err := c.enc.Encode(body); err != nil {
		log.Println("rpc codec: gob error encoding body:", err)
		return err
	}
	return nil
}

func (c *GobCodec) Close() error {
	return c.conn.Close()
}
