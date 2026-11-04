package glwm2m

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/maxid/gcoap"
	"github.com/maxid/gnety/codec/coap"
	"github.com/maxid/gnety/codec/tlv"
)

// Object 19 (Binary App Data Container) identifiers and content formats.
const (
	// BinaryAppDataObjectID is the Binary App Data Container object (Object 19).
	BinaryAppDataObjectID uint16 = 19
	// BinaryAppDataResourceID is the mandatory Data resource of Object 19.
	BinaryAppDataResourceID uint16 = 0

	// ContentFormatText is text/plain (0).
	ContentFormatText uint16 = 0
	// ContentFormatOpaque is application/octet-stream (42).
	ContentFormatOpaque uint16 = 42
	// ContentFormatTLV is application/vnd.oma.lwm2m+tlv (11542).
	ContentFormatTLV uint16 = 11542
)

// BinaryAppDataObject returns the standard LwM2M Object 19 definition:
//
//	0 Data               Opaque, R/W, multiple, mandatory
//	1 Data Priority      Integer, R/W, optional
//	2 Data Creation Time Time, R, optional
//	3 Data Description   String, R/W, optional
//	4 Data Format        String, R/W, optional
//	5 App ID             Integer, R/W, optional
//
// The Data resource holds one or more binary values, each addressed by a
// resource instance identifier.
func BinaryAppDataObject() ObjectDef {
	return ObjectDef{
		ID:       BinaryAppDataObjectID,
		Version:  "1.0",
		Multiple: true,
		Resources: map[uint16]ResourceDef{
			0: {ID: 0, Name: "Data", Operations: OpRead | OpWrite, Multiple: true, Mandatory: true, Type: ValueOpaque},
			1: {ID: 1, Name: "Data Priority (0 immediate, 1 best effort, 2 latest)", Operations: OpRead | OpWrite, Type: ValueInteger},
			2: {ID: 2, Name: "Data Creation Time", Operations: OpRead, Type: ValueTime},
			3: {ID: 3, Name: "Data Description", Operations: OpRead | OpWrite, Type: ValueString},
			4: {ID: 4, Name: "Data Format", Operations: OpRead | OpWrite, Type: ValueString},
			5: {ID: 5, Name: "App ID", Operations: OpRead | OpWrite, Type: ValueInteger},
		},
	}
}

// BinaryData is one Data resource instance of Object 19.
type BinaryData struct {
	// ID is the resource instance identifier.
	ID uint16
	// Value is the opaque payload.
	Value []byte
}

// ErrNoLocation is returned when a Create succeeded without a usable
// Location-Path.
var ErrNoLocation = errors.New("glwm2m: create response carries no instance location")

// ReadBinaryData reads the Data resource of one Object 19 instance. The device
// normally answers with a TLV MultipleResource; a plain opaque body is accepted
// as one instance with id 0.
func (s *Server) ReadBinaryData(ctx context.Context, registrationID string, instanceID uint16) ([]BinaryData, error) {
	reg, err := s.registration(registrationID)
	if err != nil {
		return nil, err
	}
	path := Path{ObjectID: BinaryAppDataObjectID, InstanceID: &instanceID, ResourceID: ptrUint16(BinaryAppDataResourceID)}
	resp, err := s.do(ctx, reg, gcoap.ClientRequest{Code: coap.CodeGET, Path: path.String()}, Operation{
		RegistrationID: registrationID, Path: path,
	})
	if err != nil {
		return nil, err
	}
	if contentFormatOf(resp.Options) != ContentFormatTLV {
		return []BinaryData{{ID: 0, Value: append([]byte(nil), resp.Payload...)}}, nil
	}
	return parseBinaryData(resp.Payload)
}

// WriteBinaryData writes the given Data resource instances of one Object 19
// instance. The payload travels as a TLV MultipleResource, so a body larger than
// the CoAP block size is uploaded with Block1 automatically. Instances not
// listed in data are left to the device's Write semantics.
func (s *Server) WriteBinaryData(ctx context.Context, registrationID string, instanceID uint16, data []BinaryData) error {
	if len(data) == 0 {
		return errors.New("glwm2m: no binary data to write")
	}
	payload, err := encodeBinaryData(data)
	if err != nil {
		return err
	}
	path := Path{ObjectID: BinaryAppDataObjectID, InstanceID: &instanceID, ResourceID: ptrUint16(BinaryAppDataResourceID)}
	return s.Write(ctx, Operation{
		RegistrationID: registrationID,
		Path:           path,
		Payload:        payload,
		ContentFormat:  ContentFormatTLV,
	})
}

// WriteBinaryDataInstance writes one Data resource instance.
func (s *Server) WriteBinaryDataInstance(ctx context.Context, registrationID string, instanceID uint16, data BinaryData) error {
	return s.WriteBinaryData(ctx, registrationID, instanceID, []BinaryData{data})
}

// CreateBinaryDataInstance creates an Object 19 instance with the given Data
// instances and returns its instance identifier. A zero identifier in the TLV
// is replaced by the device, whose Location-Path names the created instance.
func (s *Server) CreateBinaryDataInstance(ctx context.Context, registrationID string, data []BinaryData) (uint16, error) {
	container := tlv.Entry{Type: tlv.ObjectInstance, ID: 0}
	if len(data) > 0 {
		payload, err := encodeBinaryData(data)
		if err != nil {
			return 0, err
		}
		entries, err := tlv.Unmarshal(payload)
		if err != nil {
			return 0, err
		}
		container.Children = entries
	}
	payload, err := tlv.Marshal([]tlv.Entry{container})
	if err != nil {
		return 0, err
	}
	reg, err := s.registration(registrationID)
	if err != nil {
		return 0, err
	}
	path := Path{ObjectID: BinaryAppDataObjectID}.String()
	resp, err := s.do(ctx, reg, gcoap.ClientRequest{
		Code:    coap.CodePUT,
		Path:    path,
		Options: []coap.Option{contentFormatOption(ContentFormatTLV)},
		Payload: payload,
	}, Operation{RegistrationID: registrationID})
	if err != nil {
		return 0, err
	}
	if id, ok := locationInstance(resp); ok {
		return id, nil
	}
	return 0, ErrNoLocation
}

// DeleteBinaryDataInstance deletes one Object 19 instance.
func (s *Server) DeleteBinaryDataInstance(ctx context.Context, registrationID string, instanceID uint16) error {
	reg, err := s.registration(registrationID)
	if err != nil {
		return err
	}
	path := Path{ObjectID: BinaryAppDataObjectID, InstanceID: &instanceID}.String()
	_, err = s.do(ctx, reg, gcoap.ClientRequest{Code: coap.CodeDELETE, Path: path}, Operation{RegistrationID: registrationID})
	return err
}

// encodeBinaryData builds a TLV MultipleResource for the Data resource.
func encodeBinaryData(data []BinaryData) ([]byte, error) {
	children := make([]tlv.Entry, 0, len(data))
	for _, item := range data {
		children = append(children, tlv.Entry{Type: tlv.ResourceInstance, ID: item.ID, Value: append([]byte(nil), item.Value...)})
	}
	return tlv.Marshal([]tlv.Entry{{Type: tlv.MultipleResource, ID: BinaryAppDataResourceID, Children: children}})
}

// parseBinaryData extracts the Data resource instances from a TLV response.
func parseBinaryData(payload []byte) ([]BinaryData, error) {
	entries, err := tlv.Unmarshal(payload)
	if err != nil {
		return nil, fmt.Errorf("glwm2m: decode Object 19 Data: %w", err)
	}
	out := make([]BinaryData, 0, len(entries))
	for _, entry := range entries {
		switch entry.Type {
		case tlv.MultipleResource:
			if entry.ID != BinaryAppDataResourceID {
				continue
			}
			for _, child := range entry.Children {
				if child.Type != tlv.ResourceInstance {
					return nil, fmt.Errorf("glwm2m: Object 19 Data child is %v, want a resource instance", child.Type)
				}
				out = append(out, BinaryData{ID: child.ID, Value: append([]byte(nil), child.Value...)})
			}
		case tlv.ResourceValue:
			if entry.ID != BinaryAppDataResourceID {
				continue
			}
			out = append(out, BinaryData{ID: 0, Value: append([]byte(nil), entry.Value...)})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("glwm2m: Object 19 response carries no Data resource")
	}
	return out, nil
}

// locationInstance reads the last numeric segment of a Create Location-Path.
func locationInstance(resp gcoap.ClientResponse) (uint16, bool) {
	last := ""
	for _, option := range resp.Options {
		if option.Number == coap.OptionURIPath {
			last = string(option.Value)
		}
	}
	value, err := strconv.Atoi(last)
	if err != nil || value < 0 || value > 65535 {
		return 0, false
	}
	return uint16(value), true
}

func ptrUint16(value uint16) *uint16 { return &value }
