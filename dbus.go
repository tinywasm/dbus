// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package dbus

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"
)

const (
	busName      = "org.freedesktop.DBus"
	busPath      = ObjectPath("/org/freedesktop/DBus")
	busInterface = "org.freedesktop.DBus"

	methodHello       = busInterface + ".Hello"
	methodAddMatch    = busInterface + ".AddMatch"
	methodRemoveMatch = busInterface + ".RemoveMatch"

	propertiesInterface = "org.freedesktop.DBus.Properties"
	methodPropertiesGet = propertiesInterface + ".Get"

	envSessionBusAddress = "DBUS_SESSION_BUS_ADDRESS"
	envRuntimeDir        = "XDG_RUNTIME_DIR"

	callTimeout = 30 * time.Second
)

type Conn struct {
	transport  net.Conn
	uuid       string
	uniqueName string

	serialMu   sync.Mutex
	nextSerial uint32
	serialUsed map[uint32]bool

	callsMu sync.RWMutex
	calls   map[uint32]chan *Reply

	signals signalHandler

	writeMu   sync.Mutex
	closeOnce sync.Once
	closed    chan struct{}
	closeErr  error
}

func SessionBus() (*Conn, error) {
	addr, err := getSessionBusAddress()
	if err != nil {
		return nil, err
	}
	transport, err := dialAddress(addr)
	if err != nil {
		return nil, err
	}

	conn := &Conn{
		transport:  transport,
		nextSerial: 1,
		serialUsed: map[uint32]bool{0: true},
		calls:      make(map[uint32]chan *Reply),
		closed:     make(chan struct{}),
	}

	if err := conn.auth(); err != nil {
		conn.transport.Close()
		return nil, err
	}

	go conn.inWorker()

	if err := conn.hello(); err != nil {
		conn.Close()
		return nil, err
	}

	return conn, nil
}

func (c *Conn) hello() error {
	obj := c.Object(busName, busPath)
	reply := obj.Call(methodHello)
	if reply.Err != nil {
		return reply.Err
	}
	var name string
	if err := reply.Store(&name); err != nil {
		return err
	}
	c.uniqueName = name
	return nil
}

func (c *Conn) getSerial() uint32 {
	c.serialMu.Lock()
	defer c.serialMu.Unlock()
	n := c.nextSerial
	for c.serialUsed[n] {
		n++
	}
	c.serialUsed[n] = true
	c.nextSerial = n + 1
	return n
}

func (c *Conn) retireSerial(s uint32) {
	c.serialMu.Lock()
	defer c.serialMu.Unlock()
	delete(c.serialUsed, s)
}

func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.closeErr = c.transport.Close()

		c.callsMu.Lock()
		for serial, ch := range c.calls {
			delete(c.calls, serial)
			ch <- &Reply{Err: ErrClosed}
		}
		c.callsMu.Unlock()
	})
	return c.closeErr
}

func (c *Conn) inWorker() {
	for {
		msg, err := DecodeMessage(c.transport)
		if err != nil {
			c.Close()
			return
		}

		switch msg.Type {
		case TypeMethodReply:
			if serialVar, ok := msg.Headers[FieldReplySerial]; ok {
				if serial, ok := serialVar.value.(uint32); ok {
					c.callsMu.Lock()
					ch, ok := c.calls[serial]
					if ok {
						delete(c.calls, serial)
					}
					c.callsMu.Unlock()
					if ok {
						c.retireSerial(serial)
						ch <- &Reply{Body: msg.Body}
					}
				}
			}
		case TypeError:
			if serialVar, ok := msg.Headers[FieldReplySerial]; ok {
				if serial, ok := serialVar.value.(uint32); ok {
					c.callsMu.Lock()
					ch, ok := c.calls[serial]
					if ok {
						delete(c.calls, serial)
					}
					c.callsMu.Unlock()
					if ok {
						c.retireSerial(serial)
						errName, _ := msg.Headers[FieldErrorName].value.(string)
						errStr := ""
						if len(msg.Body) >= 1 {
							if s, ok := msg.Body[0].(string); ok {
								errStr = s
							}
						}
						ch <- &Reply{Err: &CallError{Name: errName, Msg: errStr}}
					}
				}
			}
		case TypeSignal:
			iface, _ := msg.Headers[FieldInterface].value.(string)
			member, _ := msg.Headers[FieldMember].value.(string)
			sender, _ := msg.Headers[FieldSender].value.(string)
			path, _ := msg.Headers[FieldPath].value.(ObjectPath)

			sig := &Signal{
				Sender: sender,
				Path:   path,
				Name:   iface + "." + member,
				Body:   msg.Body,
			}
			c.signals.deliver(sig)
		case TypeMethodCall:
			// Discard method calls per scope
		}
	}
}

type Object struct {
	conn *Conn
	dest string
	path ObjectPath
}

func (c *Conn) Object(dest string, path ObjectPath) *Object {
	return &Object{
		conn: c,
		dest: dest,
		path: path,
	}
}

func (o *Object) Call(method string, args ...any) *Reply {
	idx := strings.LastIndex(method, ".")
	if idx == -1 {
		return &Reply{Err: errors.New("dbus: invalid method name")}
	}
	iface := method[:idx]
	member := method[idx+1:]

	msg := &Message{
		Type: TypeMethodCall,
		Headers: map[HeaderField]Variant{
			FieldPath:        MakeVariant(o.path),
			FieldDestination: MakeVariant(o.dest),
			FieldInterface:   MakeVariant(iface),
			FieldMember:      MakeVariant(member),
		},
		Body: args,
	}

	if len(args) > 0 {
		msg.Headers[FieldSignature] = MakeVariant(SignatureOf(args...))
	}

	serial := o.conn.getSerial()
	msg.serial = serial

	ch := make(chan *Reply, 1)

	o.conn.callsMu.Lock()
	select {
	case <-o.conn.closed:
		o.conn.callsMu.Unlock()
		return &Reply{Err: ErrClosed}
	default:
		o.conn.calls[serial] = ch
	}
	o.conn.callsMu.Unlock()

	o.conn.writeMu.Lock()
	err := msg.EncodeTo(o.conn.transport, nativeEndian)
	o.conn.writeMu.Unlock()

	if err != nil {
		o.conn.callsMu.Lock()
		delete(o.conn.calls, serial)
		o.conn.callsMu.Unlock()
		o.conn.retireSerial(serial)
		return &Reply{Err: err}
	}

	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()

	select {
	case reply := <-ch:
		return reply
	case <-ctx.Done():
		o.conn.callsMu.Lock()
		delete(o.conn.calls, serial)
		o.conn.callsMu.Unlock()
		o.conn.retireSerial(serial)
		return &Reply{Err: ErrCallTimeout}
	case <-o.conn.closed:
		return &Reply{Err: ErrClosed}
	}
}

func (o *Object) GetProperty(iface, name string) (Variant, error) {
	reply := o.Call(methodPropertiesGet, iface, name)
	if reply.Err != nil {
		return Variant{}, reply.Err
	}
	var v Variant
	if err := reply.Store(&v); err != nil {
		return Variant{}, err
	}
	return v, nil
}

type Reply struct {
	Body []any
	Err  error
}

func Store(src []any, dest ...any) error {
	if len(src) != len(dest) {
		return ErrSignatureMismatch
	}
	for i, d := range dest {
		if err := storeValue(src[i], d); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reply) Store(dest ...any) error {
	if r.Err != nil {
		return r.Err
	}
	return Store(r.Body, dest...)
}

func storeValue(src any, dest any) error {
	dp := reflect.ValueOf(dest)
	if dp.Kind() != reflect.Ptr || dp.IsNil() {
		return ErrSignatureMismatch
	}
	target := dp.Elem()
	return storeReflect(src, target)
}

func storeReflect(src any, target reflect.Value) error {
	if !target.CanSet() {
		return ErrSignatureMismatch
	}

	if src == nil {
		return ErrSignatureMismatch
	}

	sv := reflect.ValueOf(src)

	if target.Type() == reflect.TypeOf(Variant{}) {
		if v, ok := src.(Variant); ok {
			target.Set(reflect.ValueOf(v))
			return nil
		}
		target.Set(reflect.ValueOf(MakeVariant(src)))
		return nil
	}

	if v, ok := src.(Variant); ok {
		return storeReflect(v.value, target)
	}

	if sv.Type() == target.Type() {
		target.Set(sv)
		return nil
	}

	if sv.Type().AssignableTo(target.Type()) {
		target.Set(sv)
		return nil
	}

	if sv.Type().ConvertibleTo(target.Type()) {
		target.Set(sv.Convert(target.Type()))
		return nil
	}

	if target.Kind() == reflect.Interface {
		target.Set(sv)
		return nil
	}

	if target.Kind() == reflect.Slice && sv.Kind() == reflect.Slice {
		slice := reflect.MakeSlice(target.Type(), sv.Len(), sv.Len())
		for i := 0; i < sv.Len(); i++ {
			if err := storeReflect(sv.Index(i).Interface(), slice.Index(i)); err != nil {
				return err
			}
		}
		target.Set(slice)
		return nil
	}

	if target.Kind() == reflect.Map && sv.Kind() == reflect.Map {
		m := reflect.MakeMap(target.Type())
		for _, k := range sv.MapKeys() {
			elemKey := reflect.New(target.Type().Key()).Elem()
			if err := storeReflect(k.Interface(), elemKey); err != nil {
				return err
			}
			elemVal := reflect.New(target.Type().Elem()).Elem()
			if err := storeReflect(sv.MapIndex(k).Interface(), elemVal); err != nil {
				return err
			}
			m.SetMapIndex(elemKey, elemVal)
		}
		target.Set(m)
		return nil
	}

	if target.Kind() == reflect.Struct && sv.Kind() == reflect.Slice {
		if sv.Type().Elem().Kind() == reflect.Interface {
			numFields := 0
			for i := 0; i < target.NumField(); i++ {
				field := target.Type().Field(i)
				if field.PkgPath == "" && field.Tag.Get("dbus") != "-" {
					numFields++
				}
			}
			if sv.Len() != numFields {
				return ErrSignatureMismatch
			}
			fieldIdx := 0
			for i := 0; i < target.NumField(); i++ {
				field := target.Type().Field(i)
				if field.PkgPath == "" && field.Tag.Get("dbus") != "-" {
					if err := storeReflect(sv.Index(fieldIdx).Interface(), target.Field(i)); err != nil {
						return err
					}
					fieldIdx++
				}
			}
			return nil
		}
	}

	return ErrSignatureMismatch
}
