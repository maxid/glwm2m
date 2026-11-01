// Package glwm2m implements a LwM2M server engine on top of gcoap.
//
// It owns the LwM2M protocol semantics: registration lifecycle (expiry,
// endpoint conflicts, peer binding and events), server-initiated Read/Write/
// Execute/Observe, the local object tree, TLV encoding and DTLS transport.
// Device, product and tenant identity stay in the consumer; this package only
// sees neutral registrations and peer addresses.
package glwm2m
