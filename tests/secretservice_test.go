package tests

import (
	"encoding/binary"
	"net"
	"testing"

	"github.com/tinywasm/dbus"
)

// secret mirrors org.freedesktop.Secret.Item's secret struct, signature
// (oayays). This is the exact type tinywasm/keyring's Linux backend has to put
// on the wire, so encoding it correctly is this module's reason to exist.
type secret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// TestSecretServiceCallShapes drives the seven Secret Service calls listed in
// keyring's PLAN_STAGE_4_LINUX.md §1 against a fake bus, asserting both the
// argument types the server receives and the reply types the client decodes.
//
// Every other test here covers the codec or the connection in isolation; this
// one is the acceptance criterion for the module's only consumer.
func TestSecretServiceCallShapes(t *testing.T) {
	const (
		svcName    = "org.freedesktop.secrets"
		svcPath    = dbus.ObjectPath("/org/freedesktop/secrets")
		collection = dbus.ObjectPath("/org/freedesktop/secrets/collection/login")
		itemPath   = dbus.ObjectPath("/org/freedesktop/secrets/collection/login/1")
		sessPath   = dbus.ObjectPath("/org/freedesktop/secrets/session/s1")
	)

	// What the server actually received, so the test asserts the wire shape
	// rather than just that a reply came back.
	type received struct {
		createProps map[string]dbus.Variant
		createSec   []any
		createRepl  bool
		searchAttrs map[string]string
		unlockPaths []dbus.ObjectPath
	}
	var got received

	fb := startFakeBus(t, func(conn net.Conn) {
		defer conn.Close()
		if err := serverHandshake(conn); err != nil {
			return
		}

		reply := func(serial uint32, sig string, body ...any) {
			m := &dbus.Message{
				Type: dbus.TypeMethodReply,
				Headers: map[dbus.HeaderField]dbus.Variant{
					dbus.FieldReplySerial: dbus.MakeVariant(serial),
					dbus.FieldDestination: dbus.MakeVariant(":1.100"),
					dbus.FieldSender:      dbus.MakeVariant(svcName),
				},
				Body: body,
			}
			if sig != "" {
				m.Headers[dbus.FieldSignature] = dbus.MakeVariant(dbus.ParseSignatureMust(sig))
			}
			m.EncodeTo(conn, binary.LittleEndian)
		}

		for {
			msg, err := dbus.DecodeMessage(conn)
			if err != nil {
				return
			}
			if msg.Type != dbus.TypeMethodCall {
				continue
			}
			iface, _ := msg.Headers[dbus.FieldInterface].Value().(string)
			member, _ := msg.Headers[dbus.FieldMember].Value().(string)

			switch iface + "." + member {
			case "org.freedesktop.DBus.Hello":
				reply(msg.Serial(), "s", ":1.100")

			// 1. OpenSession(s, v) -> (v, o)
			case "org.freedesktop.Secret.Service.OpenSession":
				reply(msg.Serial(), "vo", dbus.MakeVariant(""), sessPath)

			// 2. Properties.Get(s, s) -> v holding []ObjectPath
			case "org.freedesktop.DBus.Properties.Get":
				reply(msg.Serial(), "v", dbus.MakeVariant([]dbus.ObjectPath{collection}))

			// 3. Unlock(ao) -> (ao, o)
			case "org.freedesktop.Secret.Service.Unlock":
				got.unlockPaths, _ = msg.Body[0].([]dbus.ObjectPath)
				reply(msg.Serial(), "aoo", []dbus.ObjectPath{collection}, dbus.ObjectPath("/"))

			// 4. CreateItem(a{sv}, (oayays), b) -> (o, o)
			case "org.freedesktop.Secret.Collection.CreateItem":
				got.createProps, _ = msg.Body[0].(map[string]dbus.Variant)
				got.createSec, _ = msg.Body[1].([]any)
				got.createRepl, _ = msg.Body[2].(bool)
				reply(msg.Serial(), "oo", itemPath, dbus.ObjectPath("/"))

			// 5. SearchItems(a{ss}) -> (ao)
			case "org.freedesktop.Secret.Collection.SearchItems":
				got.searchAttrs, _ = msg.Body[0].(map[string]string)
				reply(msg.Serial(), "ao", []dbus.ObjectPath{itemPath})

			// 6. GetSecret(o) -> ((oayays))
			case "org.freedesktop.Secret.Item.GetSecret":
				reply(msg.Serial(), "(oayays)", secret{
					Session:     sessPath,
					Parameters:  []byte{},
					Value:       []byte("hunter2"),
					ContentType: "text/plain; charset=utf8",
				})

			// 7. Delete() -> (o)
			case "org.freedesktop.Secret.Item.Delete":
				reply(msg.Serial(), "o", dbus.ObjectPath("/"))
			}
		}
	})
	defer fb.Close()

	t.Setenv("DBUS_SESSION_BUS_ADDRESS", fb.addr)
	conn, err := dbus.SessionBus()
	if err != nil {
		t.Fatalf("SessionBus: %v", err)
	}
	defer conn.Close()

	svc := conn.Object(svcName, svcPath)

	// 1. OpenSession
	var disregard dbus.Variant
	var session dbus.ObjectPath
	if err := svc.Call("org.freedesktop.Secret.Service.OpenSession",
		"plain", dbus.MakeVariant("")).Store(&disregard, &session); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if session != sessPath {
		t.Fatalf("OpenSession session = %q, want %q", session, sessPath)
	}

	// 2. Properties.Get -> the Collections property
	v, err := svc.GetProperty("org.freedesktop.Secret.Service", "Collections")
	if err != nil {
		t.Fatalf("GetProperty: %v", err)
	}
	paths, ok := v.Value().([]dbus.ObjectPath)
	if !ok || len(paths) != 1 || paths[0] != collection {
		t.Fatalf("Collections = %#v, want [%q]", v.Value(), collection)
	}

	// 3. Unlock
	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := svc.Call("org.freedesktop.Secret.Service.Unlock",
		[]dbus.ObjectPath{collection}).Store(&unlocked, &prompt); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if len(got.unlockPaths) != 1 || got.unlockPaths[0] != collection {
		t.Fatalf("server saw Unlock(%#v), want [%q]", got.unlockPaths, collection)
	}
	if prompt != "/" {
		t.Fatalf("Unlock prompt = %q, want %q", prompt, "/")
	}

	// 4. CreateItem — the struct-in-argument case that matters most
	coll := conn.Object(svcName, collection)
	props := map[string]dbus.Variant{
		"org.freedesktop.Secret.Item.Label": dbus.MakeVariant("Password for 'u' on 'svc'"),
		"org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(map[string]string{
			"username": "u",
			"service":  "svc",
		}),
	}
	sec := secret{Session: session, Parameters: []byte{}, Value: []byte("hunter2"),
		ContentType: "text/plain; charset=utf8"}
	var item dbus.ObjectPath
	if err := coll.Call("org.freedesktop.Secret.Collection.CreateItem",
		props, sec, true).Store(&item, &prompt); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if item != itemPath {
		t.Fatalf("CreateItem item = %q, want %q", item, itemPath)
	}
	if !got.createRepl {
		t.Fatal("CreateItem replace flag did not survive the wire")
	}
	if len(got.createSec) != 4 {
		t.Fatalf("CreateItem secret arrived as %#v, want a 4-field struct", got.createSec)
	}
	if v, _ := got.createSec[2].([]byte); string(v) != "hunter2" {
		t.Fatalf("CreateItem secret value = %q, want %q", got.createSec[2], "hunter2")
	}
	if ct, _ := got.createSec[3].(string); ct != "text/plain; charset=utf8" {
		t.Fatalf("CreateItem content type = %q", ct)
	}
	// The attribute map is keyring's compatibility surface: a wrong key makes
	// every stored secret unfindable.
	attrs, _ := got.createProps["org.freedesktop.Secret.Item.Attributes"].Value().(map[string]string)
	if attrs["username"] != "u" || attrs["service"] != "svc" {
		t.Fatalf("CreateItem attributes = %#v", attrs)
	}

	// 5. SearchItems
	var results []dbus.ObjectPath
	if err := coll.Call("org.freedesktop.Secret.Collection.SearchItems",
		map[string]string{"username": "u", "service": "svc"}).Store(&results); err != nil {
		t.Fatalf("SearchItems: %v", err)
	}
	if len(results) != 1 || results[0] != itemPath {
		t.Fatalf("SearchItems = %#v, want [%q]", results, itemPath)
	}
	if got.searchAttrs["service"] != "svc" {
		t.Fatalf("server saw SearchItems(%#v)", got.searchAttrs)
	}

	// 6. GetSecret — the struct-in-reply case
	it := conn.Object(svcName, itemPath)
	var out secret
	if err := it.Call("org.freedesktop.Secret.Item.GetSecret", session).Store(&out); err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if string(out.Value) != "hunter2" {
		t.Fatalf("GetSecret value = %q, want %q", out.Value, "hunter2")
	}
	if out.Session != sessPath {
		t.Fatalf("GetSecret session = %q, want %q", out.Session, sessPath)
	}

	// 7. Delete
	if err := it.Call("org.freedesktop.Secret.Item.Delete").Store(&prompt); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if prompt != "/" {
		t.Fatalf("Delete prompt = %q, want %q", prompt, "/")
	}
}
