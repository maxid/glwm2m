package tlv

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	entries := []Entry{
		{Type: ObjectInstance, ID: 0, Children: []Entry{
			{Type: ResourceValue, ID: 5700, Value: []byte("21.5")},
			{Type: MultipleResource, ID: 6, Children: []Entry{
				{Type: ResourceInstance, ID: 0, Value: []byte{1}},
				{Type: ResourceInstance, ID: 1, Value: []byte{2}},
			}},
		}},
	}
	raw, err := Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Type != ObjectInstance || len(got[0].Children) != 2 {
		t.Fatalf("got %+v", got)
	}
	if string(got[0].Children[0].Value) != "21.5" || got[0].Children[1].Children[1].ID != 1 {
		t.Fatalf("nested mismatch: %+v", got[0])
	}
}

func TestInlineAndExtendedLengths(t *testing.T) {
	for _, value := range [][]byte{[]byte("x"), bytes.Repeat([]byte("a"), 20), bytes.Repeat([]byte("b"), 300)} {
		raw, err := Marshal([]Entry{{Type: ResourceValue, ID: 1, Value: value}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := Unmarshal(raw)
		if err != nil || !bytes.Equal(got[0].Value, value) {
			t.Fatalf("len=%d err=%v", len(value), err)
		}
	}
}

func TestMalformed(t *testing.T) {
	cases := [][]byte{{0xFF}, {0xC0, 0x01, 0x05, 0x00}}
	for i, c := range cases {
		if _, err := Unmarshal(c); err == nil {
			t.Fatalf("case %d expected error", i)
		}
	}
}
