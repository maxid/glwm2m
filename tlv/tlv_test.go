package tlv

import (
	"bytes"
	"errors"
	"testing"

	"github.com/maxid/gnety/codec/tlv"
	"github.com/maxid/gnety/codec/wire"
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
	// The codec emits children in identifier order, so resource 6 comes before
	// resource 5700: look them up by identifier instead of by position.
	resource := func(entries []Entry, id uint16) Entry {
		t.Helper()
		for _, e := range entries {
			if e.ID == id {
				return e
			}
		}
		t.Fatalf("resource %d is missing from %+v", id, entries)
		return Entry{}
	}
	if value := resource(got[0].Children, 5700).Value; string(value) != "21.5" {
		t.Fatalf("resource 5700 = %q", value)
	}
	if inner := resource(got[0].Children, 6); len(inner.Children) != 2 || inner.Children[1].ID != 1 {
		t.Fatalf("multiple resource mismatch: %+v", inner)
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

// TestDelegatesToGnety proves the shim is a shim: the same tree and the same
// payload produce the same bytes and entries as the codec it delegates to, and
// the aliases are the codec's own types.
func TestDelegatesToGnety(t *testing.T) {
	var entry Entry = tlv.Entry{Type: tlv.ResourceValue, ID: 5, Value: []byte("x")}
	_ = entry // the assignment is the alias check: Entry is tlv.Entry

	entries := []Entry{{Type: ResourceValue, ID: 2, Value: []byte("b")}, {Type: ResourceValue, ID: 1, Value: []byte("a")}}
	raw, err := Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	want, err := tlv.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, want) {
		t.Fatalf("Marshal = % x, gnety = % x", raw, want)
	}
	// Entries of one scope are emitted in identifier order.
	if !bytes.Equal(raw, []byte{0xc1, 0x01, 'a', 0xc1, 0x02, 'b'}) {
		t.Fatalf("Marshal = % x, want the canonical order", raw)
	}
	got, err := ParseLimit(raw, len(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("ParseLimit = %+v", got)
	}
}

// TestErrMalformedAlias keeps the compatibility sentinel working for callers
// that predate the classified errors.
func TestErrMalformedAlias(t *testing.T) {
	_, err := Unmarshal([]byte{0xFF})
	if err == nil {
		t.Fatal("Unmarshal accepted a malformed payload")
	}
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("error does not wrap ErrMalformed: %v", err)
	}
	if kind := wire.KindOf(err); kind != wire.KindMalformed {
		t.Fatalf("kind = %s, want malformed", kind)
	}
}
