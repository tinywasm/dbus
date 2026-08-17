// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package tests

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/tinywasm/dbus"
)

func TestDecodeArrayEmptyStruct(t *testing.T) {
	buf := bytes.NewBuffer(nil)
	msg := &dbus.Message{
		Type:  dbus.TypeMethodReply,
		Flags: 0x00,
		Headers: map[dbus.HeaderField]dbus.Variant{
			dbus.FieldDestination: dbus.MakeVariant(":1.391"),
			dbus.FieldReplySerial: dbus.MakeVariant(uint32(2)),
			dbus.FieldSignature:   dbus.MakeVariant(dbus.ParseSignatureMust("v")),
		},
		Body: []any{
			dbus.MakeVariant(struct {
				IconName    string
				Title       string
				Description string
			}{
				IconName:    "iconname",
				Title:       "title",
				Description: "description",
			}),
		},
	}
	err := msg.EncodeTo(buf, binary.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	decodedMsg, err := dbus.DecodeMessage(buf)
	if err != nil {
		t.Fatal(err)
	}
	if decodedMsg.Type != dbus.TypeMethodReply {
		t.Fatalf("expected TypeMethodReply, got %v", decodedMsg.Type)
	}
}

func TestSigByteSize(t *testing.T) {
	for sig, want := range map[string]int{
		"b":       4,
		"t":       8,
		"(yy)":    2,
		"(y(uu))": 9,
		"(y(xs))": 0,
		"s":       0,
		"ao":      0,
	} {
		if have := dbus.SigByteSize(sig); have != want {
			t.Errorf("SigByteSize(%q) = %d, want %d", sig, have, want)
		}
	}
}
