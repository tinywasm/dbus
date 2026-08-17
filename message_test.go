// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package dbus

import (
	"testing"
)

func TestMessageValidateHeader(t *testing.T) {
	var tcs = []struct {
		name string
		msg  Message
		err  string
	}{
		{
			name: "invalid flags",
			msg: Message{
				Flags: 0xFF,
			},
			err: "dbus: invalid message: invalid flags",
		},
		{
			name: "invalid message type",
			msg: Message{
				Type: 0xFF,
			},
			err: "dbus: invalid message: invalid message type",
		},
		{
			name: "invalid header",
			msg: Message{
				Type: TypeMethodCall,
				Headers: map[HeaderField]Variant{
					0xFF: MakeVariant("foo"),
				},
			},
			err: "dbus: invalid message: invalid header",
		},
		{
			name: "invalid type of header field",
			msg: Message{
				Type: TypeMethodCall,
				Headers: map[HeaderField]Variant{
					FieldPath: MakeVariant(42),
				},
			},
			err: "dbus: invalid message: invalid type of header field",
		},
		{
			name: "missing required header",
			msg: Message{
				Type: TypeMethodCall,
			},
			err: "dbus: invalid message: missing required header",
		},
		{
			name: "invalid path name",
			msg: Message{
				Type: TypeMethodCall,
				Headers: map[HeaderField]Variant{
					FieldPath:   MakeVariant(ObjectPath("break")),
					FieldMember: MakeVariant("foo.bar"),
				},
			},
			err: "dbus: invalid message: invalid path name",
		},
		{
			name: "invalid member name",
			msg: Message{
				Type: TypeMethodCall,
				Headers: map[HeaderField]Variant{
					FieldPath:   MakeVariant(ObjectPath("/")),
					FieldMember: MakeVariant("2"),
				},
			},
			err: "dbus: invalid message: invalid member name",
		},
		{
			name: "invalid interface name",
			msg: Message{
				Type: TypeMethodCall,
				Headers: map[HeaderField]Variant{
					FieldPath:      MakeVariant(ObjectPath("/")),
					FieldMember:    MakeVariant("foo.bar"),
					FieldInterface: MakeVariant("break"),
				},
			},
			err: "dbus: invalid message: invalid interface name",
		},
		{
			name: "invalid error name",
			msg: Message{
				Type: TypeError,
				Headers: map[HeaderField]Variant{
					FieldReplySerial: MakeVariant(uint32(0)),
					FieldErrorName:   MakeVariant("break"),
				},
			},
			err: "dbus: invalid message: invalid error name",
		},
		{
			name: "missing signature",
			msg: Message{
				Type: TypeError,
				Headers: map[HeaderField]Variant{
					FieldReplySerial: MakeVariant(uint32(0)),
					FieldErrorName:   MakeVariant("error.name"),
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
			err := tc.msg.validateHeader()
			if err == nil || err.Error() != tc.err {
				t.Errorf("expected error %q, got %v", tc.err, err)
			}
		})
	}
}
