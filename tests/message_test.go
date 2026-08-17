// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package tests

import (
	"testing"

	"github.com/tinywasm/dbus"
)

func TestMessageValidateHeader(t *testing.T) {
	var tcs = []struct {
		name string
		msg  dbus.Message
		err  string
	}{
		{
			name: "invalid flags",
			msg: dbus.Message{
				Flags: 0xFF,
			},
			err: "dbus: invalid message: invalid flags",
		},
		{
			name: "invalid message type",
			msg: dbus.Message{
				Type: 0xFF,
			},
			err: "dbus: invalid message: invalid message type",
		},
		{
			name: "invalid header",
			msg: dbus.Message{
				Type: dbus.TypeMethodCall,
				Headers: map[dbus.HeaderField]dbus.Variant{
					0xFF: dbus.MakeVariant("foo"),
				},
			},
			err: "dbus: invalid message: invalid header",
		},
		{
			name: "invalid type of header field",
			msg: dbus.Message{
				Type: dbus.TypeMethodCall,
				Headers: map[dbus.HeaderField]dbus.Variant{
					dbus.FieldPath: dbus.MakeVariant(42),
				},
			},
			err: "dbus: invalid message: invalid type of header field",
		},
		{
			name: "missing required header",
			msg: dbus.Message{
				Type: dbus.TypeMethodCall,
			},
			err: "dbus: invalid message: missing required header",
		},
		{
			name: "invalid path name",
			msg: dbus.Message{
				Type: dbus.TypeMethodCall,
				Headers: map[dbus.HeaderField]dbus.Variant{
					dbus.FieldPath:   dbus.MakeVariant(dbus.ObjectPath("break")),
					dbus.FieldMember: dbus.MakeVariant("foo.bar"),
				},
			},
			err: "dbus: invalid message: invalid path name",
		},
		{
			name: "invalid member name",
			msg: dbus.Message{
				Type: dbus.TypeMethodCall,
				Headers: map[dbus.HeaderField]dbus.Variant{
					dbus.FieldPath:   dbus.MakeVariant(dbus.ObjectPath("/")),
					dbus.FieldMember: dbus.MakeVariant("2"),
				},
			},
			err: "dbus: invalid message: invalid member name",
		},
		{
			name: "invalid interface name",
			msg: dbus.Message{
				Type: dbus.TypeMethodCall,
				Headers: map[dbus.HeaderField]dbus.Variant{
					dbus.FieldPath:      dbus.MakeVariant(dbus.ObjectPath("/")),
					dbus.FieldMember:    dbus.MakeVariant("foo.bar"),
					dbus.FieldInterface: dbus.MakeVariant("break"),
				},
			},
			err: "dbus: invalid message: invalid interface name",
		},
		{
			name: "invalid error name",
			msg: dbus.Message{
				Type: dbus.TypeError,
				Headers: map[dbus.HeaderField]dbus.Variant{
					dbus.FieldReplySerial: dbus.MakeVariant(uint32(0)),
					dbus.FieldErrorName:   dbus.MakeVariant("break"),
				},
			},
			err: "dbus: invalid message: invalid error name",
		},
		{
			name: "missing signature",
			msg: dbus.Message{
				Type: dbus.TypeError,
				Headers: map[dbus.HeaderField]dbus.Variant{
					dbus.FieldReplySerial: dbus.MakeVariant(uint32(0)),
					dbus.FieldErrorName:   dbus.MakeVariant("error.name"),
				},
				Body: []any{
					"break",
				},
			},
			err: "dbus: invalid message: missing signature",
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			err := dbus.ValidateHeader(&tc.msg)
			if err == nil || err.Error() != tc.err {
				t.Errorf("expected error %q, got %v", tc.err, err)
			}
		})
	}
}
