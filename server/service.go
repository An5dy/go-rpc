package server

import (
	"go/ast"
	"log"
	"reflect"
	"sync/atomic"
)

// methodType 方法类型结构体
type methodType struct {
	method    reflect.Method // 方法本身
	ArgType   reflect.Type   // 第一个参数的类型
	ReplyType reflect.Type   // 第二个参数的类型
	numCalls  uint64         // 后续统计方法调用次数时会用到
}

// NumCalls 原子获取调用次数
func (m *methodType) NumCalls() uint64 {
	return atomic.LoadUint64(&m.numCalls)
}

// newArgv 创建参数类型
func (m *methodType) newArgv() reflect.Value {
	var argv reflect.Value
	if m.ArgType.Kind() == reflect.Ptr {
		// 创建指针类型
		argv = reflect.New(m.ArgType.Elem())
	} else {
		// 创建值类型
		argv = reflect.New(m.ArgType).Elem()
	}
	return argv
}

// newReplyv 创建返回值，必须为指针
func (m *methodType) newReplyv() reflect.Value {
	replyv := reflect.New(m.ReplyType.Elem())
	switch m.ReplyType.Elem().Kind() {
	case reflect.Map:
		replyv.Elem().Set(reflect.MakeMap(m.ReplyType.Elem()))
	case reflect.Slice:
		replyv.Elem().Set(reflect.MakeSlice(m.ReplyType.Elem(), 0, 0))
	}
	return replyv
}

// service 服务结构体
type service struct {
	name   string                 // 映射的结构体的名称
	typ    reflect.Type           // 结构体的类型
	rcvr   reflect.Value          // 结构体的实例本身，调用时需要 rcvr 作为第 0 个参数
	method map[string]*methodType // 存储映射的结构体的所有符合条件的方法
}

// newService 构建 service 实例
func newService(rcvr interface{}) *service {
	s := new(service)
	s.rcvr = reflect.ValueOf(rcvr)
	s.name = reflect.Indirect(s.rcvr).Type().Name()
	s.typ = reflect.TypeOf(rcvr)
	if !ast.IsExported(s.name) {
		log.Fatalf("rpc server: %s is not a valid service name", s.name)
	}
	s.registerMethods()
	return s
}

// registerMethods 通过反射注册服务
func (s *service) registerMethods() {
	s.method = make(map[string]*methodType)
	for i := 0; i < s.typ.NumMethod(); i++ {
		method := s.typ.Method(i)
		mType := method.Type
		// 如果结构体方法入参不等于3个或者返回值不等于1个，则该方法不满足
		if mType.NumIn() != 3 || mType.NumOut() != 1 {
			continue
		}
		// 如果返回参数类型不为 error，则该方法不满足
		if mType.Out(0) != reflect.TypeOf((*error)(nil)).Elem() {
			continue
		}
		argType, replyType := mType.In(1), mType.In(2)
		if !isExportedOrBuiltinType(argType) || !isExportedOrBuiltinType(replyType) {
			continue
		}
		// 设置满足条件的方法
		s.method[method.Name] = &methodType{
			method:    method,
			ArgType:   argType,
			ReplyType: replyType,
		}
		log.Printf("rpc server: register %s.%s\n", s.name, method.Name)
	}
}

// call 通过反射值调用方法
func (s *service) call(m *methodType, argv, replyv reflect.Value) error {
	atomic.AddUint64(&m.numCalls, 1)
	f := m.method.Func
	returnValues := f.Call([]reflect.Value{s.rcvr, argv, replyv})
	if errInter := returnValues[0].Interface(); errInter != nil {
		return errInter.(error)
	}
	return nil
}

// isExportedOrBuiltinType 是否是可以被访问或内置的数据类型
func isExportedOrBuiltinType(t reflect.Type) bool {
	return ast.IsExported(t.Name()) || t.PkgPath() == ""
}
