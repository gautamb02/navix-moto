// Package aap implements the Android Auto control channel messages.
//
// Message IDs and layouts come from aasdk / Open Headunit and must be
// verified against a real phone; Google does not publish the protocol.
package aap

import (
	"encoding/binary"
	"fmt"
)

// ChannelControl is the channel ID used for version, auth and discovery.
const ChannelControl byte = 0

// Control channel message IDs (first 2 bytes of every payload).
const (
	MsgVersionRequest  uint16 = 0x0001
	MsgVersionResponse uint16 = 0x0002
	MsgSSLHandshake    uint16 = 0x0003
	MsgAuthComplete    uint16 = 0x0004
)

// Protocol version we advertise.
const (
	VersionMajor uint16 = 1
	VersionMinor uint16 = 1
)

// VersionRequest builds the payload for the version request: message ID
// followed by major and minor version, all big-endian.
func VersionRequest() []byte {
	b := make([]byte, 6)
	binary.BigEndian.PutUint16(b[0:], MsgVersionRequest)
	binary.BigEndian.PutUint16(b[2:], VersionMajor)
	binary.BigEndian.PutUint16(b[4:], VersionMinor)
	return b
}

// VersionResponse is the phone's reply to the version request.
type VersionResponse struct {
	Major, Minor uint16
	Status       uint16 // 0 = OK
}

// ParseVersionResponse decodes a version response payload.
func ParseVersionResponse(p []byte) (VersionResponse, error) {
	if len(p) < 8 {
		return VersionResponse{}, fmt.Errorf("version response too short: %d bytes", len(p))
	}
	if id := binary.BigEndian.Uint16(p); id != MsgVersionResponse {
		return VersionResponse{}, fmt.Errorf("unexpected message id %#04x", id)
	}
	return VersionResponse{
		Major:  binary.BigEndian.Uint16(p[2:]),
		Minor:  binary.BigEndian.Uint16(p[4:]),
		Status: binary.BigEndian.Uint16(p[6:]),
	}, nil
}

// AuthCompleteOK is the protobuf body of an auth complete message with
// status OK: field 1 (varint) = 0.
var AuthCompleteOK = []byte{0x08, 0x00}

// WithID prefixes body with the 2-byte big-endian message ID.
func WithID(id uint16, body []byte) []byte {
	b := make([]byte, 2, 2+len(body))
	binary.BigEndian.PutUint16(b, id)
	return append(b, body...)
}

// SplitID separates the 2-byte message ID from the rest of a payload.
func SplitID(p []byte) (uint16, []byte, error) {
	if len(p) < 2 {
		return 0, nil, fmt.Errorf("payload too short: %d bytes", len(p))
	}
	return binary.BigEndian.Uint16(p), p[2:], nil
}
