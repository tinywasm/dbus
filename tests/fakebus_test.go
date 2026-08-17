package tests

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tinywasm/dbus"
)

type fakeBus struct {
	listener net.Listener
	addr     string
	handler  func(net.Conn)
}

func startFakeBus(t *testing.T, handler func(net.Conn)) *fakeBus {
	t.Helper()
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "bus.sock")

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("failed to listen on unix socket: %v", err)
	}

	fb := &fakeBus{
		listener: l,
		addr:     "unix:path=" + sockPath,
		handler:  handler,
	}

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go handler(conn)
		}
	}()

	return fb
}

func (fb *fakeBus) Close() {
	fb.listener.Close()
}

func readByte(r io.Reader) (byte, error) {
	var b [1]byte
	_, err := io.ReadFull(r, b[:])
	return b[0], err
}

func readLineRaw(r io.Reader) ([]byte, error) {
	var buf []byte
	for {
		b, err := readByte(r)
		if err != nil {
			return nil, err
		}
		buf = append(buf, b)
		if len(buf) >= 2 && buf[len(buf)-2] == '\r' && buf[len(buf)-1] == '\n' {
			return bytes.TrimSuffix(buf, []byte("\r\n")), nil
		}
	}
}

func readAuthLineRaw(r io.Reader) ([][]byte, error) {
	line, err := readLineRaw(r)
	if err != nil {
		return nil, err
	}
	return bytes.Split(line, []byte{' '}), nil
}

func serverHandshake(conn net.Conn) error {
	nul, err := readByte(conn)
	if err != nil || nul != 0 {
		return errors.New("expected NUL byte")
	}

	line, err := readAuthLineRaw(conn)
	if err != nil {
		return err
	}
	if len(line) < 1 || string(line[0]) != "AUTH" {
		return errors.New("expected AUTH")
	}

	uid := strconv.Itoa(os.Geteuid())
	uidHex := hex.EncodeToString([]byte(uid))

	resp := "REJECTED EXTERNAL\r\n"
	conn.Write([]byte(resp))

	line, err = readAuthLineRaw(conn)
	if err != nil {
		return err
	}
	if len(line) < 3 || string(line[0]) != "AUTH" || string(line[1]) != "EXTERNAL" || string(line[2]) != uidHex {
		conn.Write([]byte("REJECTED\r\n"))
		return errors.New("invalid client auth data")
	}

	conn.Write([]byte("OK 1234567890abcdef1234567890abcdef\r\n"))

	line, err = readAuthLineRaw(conn)
	if err != nil {
		return err
	}
	if len(line) < 1 || string(line[0]) != "BEGIN" {
		return errors.New("expected BEGIN")
	}

	return nil
}

func TestFakeBusSessionBus(t *testing.T) {
	fb := startFakeBus(t, func(conn net.Conn) {
		defer conn.Close()
		if err := serverHandshake(conn); err != nil {
			return
		}

		for {
			msg, err := dbus.DecodeMessage(conn)
			if err != nil {
				return
			}
			if msg.Type == dbus.TypeMethodCall {
				iface, _ := msg.Headers[dbus.FieldInterface].Value().(string)
				member, _ := msg.Headers[dbus.FieldMember].Value().(string)
				if iface == "org.freedesktop.DBus" && member == "Hello" {
					reply := &dbus.Message{
						Type: dbus.TypeMethodReply,
						Headers: map[dbus.HeaderField]dbus.Variant{
							dbus.FieldReplySerial: dbus.MakeVariant(msg.Serial()),
							dbus.FieldDestination: dbus.MakeVariant(":1.100"),
							dbus.FieldSender:      dbus.MakeVariant("org.freedesktop.DBus"),
							dbus.FieldSignature:   dbus.MakeVariant(dbus.ParseSignatureMust("s")),
						},
						Body: []any{":1.100"},
					}
					reply.EncodeTo(conn, binary.LittleEndian)
				} else if iface == "org.example.Test" && member == "Echo" {
					str := msg.Body[0].(string)
					reply := &dbus.Message{
						Type: dbus.TypeMethodReply,
						Headers: map[dbus.HeaderField]dbus.Variant{
							dbus.FieldReplySerial: dbus.MakeVariant(msg.Serial()),
							dbus.FieldDestination: dbus.MakeVariant(":1.100"),
							dbus.FieldSender:      dbus.MakeVariant(":1.200"),
							dbus.FieldSignature:   dbus.MakeVariant(dbus.ParseSignatureMust("s")),
						},
						Body: []any{str},
					}
					reply.EncodeTo(conn, binary.LittleEndian)
				} else if iface == "org.freedesktop.DBus.Properties" && member == "Get" {
					reply := &dbus.Message{
						Type: dbus.TypeMethodReply,
						Headers: map[dbus.HeaderField]dbus.Variant{
							dbus.FieldReplySerial: dbus.MakeVariant(msg.Serial()),
							dbus.FieldDestination: dbus.MakeVariant(":1.100"),
							dbus.FieldSender:      dbus.MakeVariant(":1.200"),
							dbus.FieldSignature:   dbus.MakeVariant(dbus.ParseSignatureMust("v")),
						},
						Body: []any{dbus.MakeVariant("prop_value")},
					}
					reply.EncodeTo(conn, binary.LittleEndian)
				}
			}
		}
	})
	defer fb.Close()

	os.Setenv("DBUS_SESSION_BUS_ADDRESS", fb.addr)

	conn, err := dbus.SessionBus()
	if err != nil {
		t.Fatalf("SessionBus failed: %v", err)
	}
	defer conn.Close()

	obj := conn.Object("org.example.Test", dbus.ObjectPath("/org/example/Test"))
	reply := obj.Call("org.example.Test.Echo", "hello")
	if reply.Err != nil {
		t.Fatalf("Call Echo failed: %v", reply.Err)
	}
	var res string
	if err := reply.Store(&res); err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if res != "hello" {
		t.Fatalf("expected 'hello', got %q", res)
	}

	prop, err := obj.GetProperty("org.example.Test", "SomeProp")
	if err != nil {
		t.Fatalf("GetProperty failed: %v", err)
	}
	if prop.Value() != "prop_value" {
		t.Fatalf("expected 'prop_value', got %v", prop.Value())
	}
}

func TestFakeBusAuthFailed(t *testing.T) {
	fb := startFakeBus(t, func(conn net.Conn) {
		defer conn.Close()
		readByte(conn)
		readLineRaw(conn)
		conn.Write([]byte("REJECTED SOME_OTHER_MECH\r\n"))
	})
	defer fb.Close()

	os.Setenv("DBUS_SESSION_BUS_ADDRESS", fb.addr)

	_, err := dbus.SessionBus()
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
	if !errors.Is(err, dbus.ErrAuthFailed) {
		t.Fatalf("expected ErrAuthFailed, got %v", err)
	}
}

func TestFakeBusCloseMidCall(t *testing.T) {
	fb := startFakeBus(t, func(conn net.Conn) {
		if err := serverHandshake(conn); err != nil {
			conn.Close()
			return
		}
		msg, err := dbus.DecodeMessage(conn)
		if err == nil && msg.Type == dbus.TypeMethodCall {
			reply := &dbus.Message{
				Type: dbus.TypeMethodReply,
				Headers: map[dbus.HeaderField]dbus.Variant{
					dbus.FieldReplySerial: dbus.MakeVariant(msg.Serial()),
					dbus.FieldDestination: dbus.MakeVariant(":1.100"),
					dbus.FieldSender:      dbus.MakeVariant("org.freedesktop.DBus"),
					dbus.FieldSignature:   dbus.MakeVariant(dbus.ParseSignatureMust("s")),
				},
				Body: []any{":1.100"},
			}
			reply.EncodeTo(conn, binary.LittleEndian)
		}
		msg, err = dbus.DecodeMessage(conn)
		_ = msg
		conn.Close()
	})
	defer fb.Close()

	os.Setenv("DBUS_SESSION_BUS_ADDRESS", fb.addr)

	conn, err := dbus.SessionBus()
	if err != nil {
		t.Fatalf("SessionBus failed: %v", err)
	}
	defer conn.Close()

	obj := conn.Object("org.example.Test", dbus.ObjectPath("/org/example/Test"))
	reply := obj.Call("org.example.Test.LongRunning")
	if reply.Err == nil || !errors.Is(reply.Err, dbus.ErrClosed) {
		t.Fatalf("expected ErrClosed, got %v", reply.Err)
	}
}

func TestFakeBusSignals(t *testing.T) {
	var serverConn net.Conn
	var serverMu sync.Mutex

	fb := startFakeBus(t, func(conn net.Conn) {
		if err := serverHandshake(conn); err != nil {
			conn.Close()
			return
		}
		serverMu.Lock()
		serverConn = conn
		serverMu.Unlock()

		for {
			msg, err := dbus.DecodeMessage(conn)
			if err != nil {
				return
			}
			if msg.Type == dbus.TypeMethodCall {
				reply := &dbus.Message{
					Type: dbus.TypeMethodReply,
					Headers: map[dbus.HeaderField]dbus.Variant{
						dbus.FieldReplySerial: dbus.MakeVariant(msg.Serial()),
						dbus.FieldDestination: dbus.MakeVariant(":1.100"),
						dbus.FieldSender:      dbus.MakeVariant("org.freedesktop.DBus"),
					},
				}
				iface, _ := msg.Headers[dbus.FieldInterface].Value().(string)
				member, _ := msg.Headers[dbus.FieldMember].Value().(string)
				if iface == "org.freedesktop.DBus" && member == "Hello" {
					reply.Headers[dbus.FieldSignature] = dbus.MakeVariant(dbus.ParseSignatureMust("s"))
					reply.Body = []any{":1.100"}
				}
				reply.EncodeTo(conn, binary.LittleEndian)
			}
		}
	})
	defer fb.Close()

	os.Setenv("DBUS_SESSION_BUS_ADDRESS", fb.addr)

	conn, err := dbus.SessionBus()
	if err != nil {
		t.Fatalf("SessionBus failed: %v", err)
	}
	defer conn.Close()

	sigChan := make(chan *dbus.Signal, 5)
	conn.Signals(sigChan)

	if err := conn.AddMatch("type='signal'"); err != nil {
		t.Fatalf("AddMatch failed: %v", err)
	}

	serverMu.Lock()
	sc := serverConn
	serverMu.Unlock()

	sigMsg := &dbus.Message{
		Type: dbus.TypeSignal,
		Headers: map[dbus.HeaderField]dbus.Variant{
			dbus.FieldPath:      dbus.MakeVariant(dbus.ObjectPath("/org/example/Signal")),
			dbus.FieldInterface: dbus.MakeVariant("org.example.Signal"),
			dbus.FieldMember:    dbus.MakeVariant("OnEvent"),
			dbus.FieldSender:    dbus.MakeVariant(":1.200"),
			dbus.FieldSignature: dbus.MakeVariant(dbus.ParseSignatureMust("s")),
		},
		Body: []any{"event_data"},
	}
	sigMsg.EncodeTo(sc, binary.LittleEndian)

	select {
	case sig := <-sigChan:
		if sig.Name != "org.example.Signal.OnEvent" {
			t.Fatalf("unexpected signal name: %s", sig.Name)
		}
		if len(sig.Body) != 1 || sig.Body[0] != "event_data" {
			t.Fatalf("unexpected signal body: %v", sig.Body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for signal")
	}

	if err := conn.RemoveMatch("type='signal'"); err != nil {
		t.Fatalf("RemoveMatch failed: %v", err)
	}
}
