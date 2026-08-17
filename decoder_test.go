// Portions Copyright (c) 2013, Georg Reinke, Google — BSD-2-Clause

package dbus

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestDecodeArrayEmptyStruct(t *testing.T) {
	buf := bytes.NewBuffer(nil)
	msg := &Message{
		Type:  TypeMethodReply,
		Flags: 0x00,
		Headers: map[HeaderField]Variant{
			FieldDestination: MakeVariant(":1.391"),
			FieldReplySerial: MakeVariant(uint32(2)),
			FieldSignature:   MakeVariant(ParseSignatureMust("v")),
		},
		Body: []any{
			MakeVariant(struct {
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
	decodedMsg, err := DecodeMessage(buf)
	if err != nil {
		t.Fatal(err)
	}
	if decodedMsg.Type != TypeMethodReply {
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
		if have := sigByteSize(sig); have != want {
			t.Errorf("sigByteSize(%q) = %d, want %d", sig, have, want)
		}
	}
}
