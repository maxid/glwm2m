package glwm2m

import (
	"bytes"
	"context"
	"net/url"
	"testing"

	"github.com/maxid/gcoap"
	"github.com/maxid/gnety/codec/coap"
	"github.com/maxid/gnety/codec/tlv"
)

// FuzzDecodeTLV feeds peer-controlled bytes through the LwM2M TLV path: the
// codec, and then what the server does with the result — the write path that
// turns entries into object values.
//
// The codec has its own fuzz target in gnety; this one covers the layer above it,
// which a device drives with a Write body:
//
//   - no panic, in the codec or in the write path;
//   - what parses re-encodes canonically, so two round trips produce one form;
//   - no entry or stored value reports more bytes than the input carried;
//   - a body leaves at most one value per (object, instance, resource), so the
//     store cannot grow faster than the request that filled it.
func FuzzDecodeTLV(f *testing.F) {
	seed := func(entries ...tlv.Entry) {
		raw, err := tlv.Marshal(entries)
		if err != nil {
			f.Fatalf("marshal seed: %v", err)
		}
		f.Add(raw)
	}
	f.Add([]byte{})
	seed(tlv.Entry{Type: tlv.ResourceValue, ID: 5700, Value: []byte("21.5")})
	seed(tlv.Entry{Type: tlv.ObjectInstance, ID: 0, Children: []tlv.Entry{
		{Type: tlv.ResourceValue, ID: 5700, Value: []byte("21.5")},
		{Type: tlv.ResourceValue, ID: 5701, Value: []byte("C")},
	}})
	seed(tlv.Entry{Type: tlv.MultipleResource, ID: 11, Children: []tlv.Entry{
		{Type: tlv.ResourceInstance, ID: 0, Value: []byte("a")},
		{Type: tlv.ResourceInstance, ID: 1, Value: []byte("b")},
	}})
	// A header that promises more bytes than the datagram carries.
	f.Add([]byte{0xc1, 0x16, 0x44, 0x00})

	f.Fuzz(func(t *testing.T, data []byte) {
		entries, err := tlv.Unmarshal(data)
		if err != nil {
			return // a rejected body is the expected outcome for most inputs
		}
		raw, err := tlv.Marshal(entries)
		if err != nil {
			t.Fatalf("Marshal of the entries parsed from % x failed: %v", data, err)
		}
		again, err := tlv.Unmarshal(raw)
		if err != nil {
			t.Fatalf("Unmarshal of the re-encoded entries % x failed: %v", raw, err)
		}
		raw2, err := tlv.Marshal(again)
		if err != nil {
			t.Fatalf("Marshal of the re-parsed entries % x failed: %v", raw, err)
		}
		if !bytes.Equal(raw, raw2) {
			t.Fatalf("encode(decode(x)) is not stable:\ninput  % x\nfirst  % x\nsecond % x", data, raw, raw2)
		}
		if got := valueBytes(entries); got > len(data) {
			t.Fatalf("decoded %d value bytes from a %d-byte body", got, len(data))
		}

		// The write path: a fresh server per input so the bound below is the
		// body's, not an accumulation across iterations.
		srv, err := New(Config{Addr: "127.0.0.1:0"})
		if err != nil {
			t.Fatalf("new server: %v", err)
		}
		updates, err := srv.collectUpdates(3303, nil, entries)
		if err != nil {
			return // rejected writes are fine; the properties above still held
		}
		srv.applyUpdates(updates)
		stored := 0
		for _, instances := range srv.store.snapshot() {
			for _, instance := range instances {
				stored += len(instance.Value)
			}
		}
		if stored > len(data) {
			t.Fatalf("stored %d value bytes from a %d-byte body", stored, len(data))
		}
		for _, update := range updates {
			if len(update.value) > len(data) {
				t.Fatalf("one update carries %d bytes from a %d-byte body", len(update.value), len(data))
			}
		}
	})
}

// valueBytes sums the leaf values of a decoded TLV tree.
func valueBytes(entries []tlv.Entry) int {
	total := 0
	for _, entry := range entries {
		total += len(entry.Value)
		total += valueBytes(entry.Children)
	}
	return total
}

// FuzzDecodeRegistration drives registration requests with peer-controlled
// fields: the endpoint name, the lifetime, the binding, the version and the
// object-link body.
//
// It pins the registration contract: every input ends in a defined answer, an
// accepted registration keeps the endpoint exactly and stays within the lifetime
// bound, a rejected one leaves no state behind, and nothing a client sends can
// produce more links than bytes it sent.
func FuzzDecodeRegistration(f *testing.F) {
	f.Add("dev-1", "60", "U", "1.0", []byte("</1/0>,</3303/0>"))
	f.Add("dev-2", "", "", "", []byte(nil))
	f.Add("", "60", "U", "1.0", []byte(nil))
	f.Add("dev-3", "0", "U", "1.0", []byte(nil))
	f.Add("dev-4", "-1", "X", "2.0", []byte("</3303/0>"))
	f.Add("dev-5", "999999999999", "UQ", "1.0", []byte("</3303/0>,</3303/0>"))

	f.Fuzz(func(t *testing.T, endpoint, lifetime, binding, version string, body []byte) {
		srv, err := New(Config{Addr: "127.0.0.1:0"})
		if err != nil {
			t.Fatalf("new server: %v", err)
		}
		query := url.Values{}
		query.Set("ep", endpoint)
		query.Set("lt", lifetime)
		query.Set("b", binding)
		query.Set("lwm2m", version)
		resp := srv.handleRegister(context.Background(), gcoap.Request{Query: query, Payload: body})

		switch resp.Code {
		case coap.CodeCreated:
			if got := srv.Count(); got != 1 {
				t.Fatalf("accepted registration left %d registrations, want 1", got)
			}
			regs := srv.Registrations()
			if len(regs) != 1 {
				t.Fatalf("Registrations() = %d entries, want 1", len(regs))
			}
			reg := regs[0]
			if reg.Endpoint != endpoint {
				t.Fatalf("stored endpoint = %q, want %q", reg.Endpoint, endpoint)
			}
			if max := int(srv.cfg.MaxLifetime.Seconds()); reg.Lifetime > max {
				t.Fatalf("lifetime = %ds exceeds the cap %ds", reg.Lifetime, max)
			}
			if len(reg.ObjectLinks) > len(body) {
				t.Fatalf("accepted %d links from a %d-byte body", len(reg.ObjectLinks), len(body))
			}
			for _, link := range reg.ObjectLinks {
				if len(link) > len(body) {
					t.Fatalf("one link is %d bytes from a %d-byte body", len(link), len(body))
				}
			}
		case coap.CodeBadRequest:
			if got := srv.Count(); got != 0 {
				t.Fatalf("rejected registration left %d registrations behind", got)
			}
		default:
			t.Fatalf("registration answered %s, want 2.01 or 4.00", resp.Code)
		}
	})
}
