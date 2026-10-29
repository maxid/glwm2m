// Package tlv implements the OMA LwM2M TLV wire format.
package tlv

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Type is the LwM2M TLV type.
type Type uint8

const (
	ObjectInstance   Type = 0
	ResourceInstance Type = 1
	MultipleResource Type = 2
	ResourceValue    Type = 3
)

// Entry is one TLV entry. Containers use Children; leaves use Value.
type Entry struct {
	Type     Type
	ID       uint16
	Value    []byte
	Children []Entry
}

var ErrMalformed = errors.New("lwm2m tlv: malformed")

// Marshal encodes entries.
func Marshal(entries []Entry) ([]byte, error) {
	var out []byte
	for _, e := range entries {
		b, err := encodeEntry(e)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

func encodeEntry(e Entry) ([]byte, error) {
	payload := e.Value
	if e.Type == ObjectInstance || e.Type == MultipleResource {
		var err error
		payload, err = Marshal(e.Children)
		if err != nil {
			return nil, err
		}
	}
	if e.Type == ResourceValue || e.Type == ResourceInstance {
		if len(e.Children) > 0 {
			return nil, fmt.Errorf("%w: leaf has children", ErrMalformed)
		}
	}
	length := len(payload)
	if length > 0xFFFFFF {
		return nil, fmt.Errorf("%w: value too large", ErrMalformed)
	}

	header := byte(e.Type << 6)
	if e.ID > 0xFF {
		header |= 0x20
	}
	switch {
	case length < 8:
		// inline 3-bit length
	case length < 0x100:
		header |= 0x08
	case length < 0x10000:
		header |= 0x10
	default:
		header |= 0x18
	}
	if length < 8 {
		header |= byte(length)
	}
	out := []byte{header}
	if e.ID > 0xFF {
		out = append(out, byte(e.ID>>8), byte(e.ID))
	} else {
		out = append(out, byte(e.ID))
	}
	switch {
	case length < 8:
	case length < 0x100:
		out = append(out, byte(length))
	case length < 0x10000:
		out = append(out, byte(length>>8), byte(length))
	default:
		out = append(out, byte(length>>16), byte(length>>8), byte(length))
	}
	return append(out, payload...), nil
}

// Unmarshal decodes a TLV byte slice.
func Unmarshal(data []byte) ([]Entry, error) {
	entries, rest, err := decodeAll(data)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, ErrMalformed
	}
	return entries, nil
}

func decodeAll(data []byte) ([]Entry, []byte, error) {
	var out []Entry
	for len(data) > 0 {
		e, rest, err := decodeOne(data)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, e)
		data = rest
	}
	return out, data, nil
}

func decodeOne(data []byte) (Entry, []byte, error) {
	var e Entry
	if len(data) < 2 {
		return e, nil, ErrMalformed
	}
	header := data[0]
	e.Type = Type(header >> 6)
	if e.Type > ResourceValue {
		return e, nil, ErrMalformed
	}
	pos := 1
	if header&0x20 != 0 {
		if len(data) < pos+2 {
			return e, nil, ErrMalformed
		}
		e.ID = binary.BigEndian.Uint16(data[pos:])
		pos += 2
	} else {
		e.ID = uint16(data[pos])
		pos++
	}
	lengthType := header & 0x18
	length := 0
	switch lengthType {
	case 0x00:
		length = int(header & 0x07)
	case 0x08:
		if len(data) < pos+1 {
			return e, nil, ErrMalformed
		}
		length = int(data[pos])
		pos++
	case 0x10:
		if len(data) < pos+2 {
			return e, nil, ErrMalformed
		}
		length = int(binary.BigEndian.Uint16(data[pos:]))
		pos += 2
	case 0x18:
		if len(data) < pos+3 {
			return e, nil, ErrMalformed
		}
		length = int(data[pos])<<16 | int(binary.BigEndian.Uint16(data[pos+1:]))
		pos += 3
	}
	if length < 0 || len(data) < pos+length {
		return e, nil, ErrMalformed
	}
	payload := data[pos : pos+length]
	rest := data[pos+length:]
	switch e.Type {
	case ResourceValue, ResourceInstance:
		e.Value = append([]byte(nil), payload...)
	case ObjectInstance, MultipleResource:
		children, innerRest, err := decodeAll(payload)
		if err != nil {
			return e, nil, err
		}
		if len(innerRest) != 0 {
			return e, nil, ErrMalformed
		}
		e.Children = children
	}
	return e, rest, nil
}
