// Package transport implements the Android Auto frame layer: a 4-byte header
// (channel, flags, big-endian payload length) with optional fragmentation.
package transport

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Frame flag bits.
const (
	FlagFirst     = 0x01 // first fragment of a message
	FlagLast      = 0x02 // last fragment of a message
	FlagControl   = 0x04 // control message rather than channel-specific
	FlagEncrypted = 0x08 // payload is encrypted with the TLS session
)

// MaxFragment is the largest payload carried in a single frame.
const MaxFragment = 0x4000

// Frame is one wire frame. Payload is the raw (possibly encrypted) bytes.
// TotalSize is only present on the first fragment of a multi-frame message.
type Frame struct {
	Channel   byte
	Flags     byte
	Payload   []byte
	TotalSize uint32
}

func (f Frame) hasTotalSize() bool {
	return f.Flags&(FlagFirst|FlagLast) == FlagFirst
}

// WriteFrame encodes f onto w.
func WriteFrame(w io.Writer, f Frame) error {
	if len(f.Payload) > 0xFFFF {
		return fmt.Errorf("payload too large: %d", len(f.Payload))
	}
	// The length field counts only the payload; the total size on a first
	// fragment follows the header in addition to it.
	buf := make([]byte, 0, 8+len(f.Payload))
	buf = append(buf, f.Channel, f.Flags)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(f.Payload)))
	if f.hasTotalSize() {
		buf = binary.BigEndian.AppendUint32(buf, f.TotalSize)
	}
	buf = append(buf, f.Payload...)
	_, err := w.Write(buf)
	return err
}

// ReadFrame decodes one frame from r.
func ReadFrame(r io.Reader) (Frame, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	f := Frame{Channel: hdr[0], Flags: hdr[1]}
	n := int(binary.BigEndian.Uint16(hdr[2:]))
	if f.hasTotalSize() {
		var sz [4]byte
		if _, err := io.ReadFull(r, sz[:]); err != nil {
			return Frame{}, err
		}
		f.TotalSize = binary.BigEndian.Uint32(sz[:])
	}
	f.Payload = make([]byte, n)
	if _, err := io.ReadFull(r, f.Payload); err != nil {
		return Frame{}, err
	}
	return f, nil
}

// Message is a fully reassembled message on one channel.
type Message struct {
	Channel   byte
	Control   bool
	Encrypted bool
	Payload   []byte
}

// Reader reassembles fragmented messages from a stream of frames.
type Reader struct {
	r       io.Reader
	partial map[byte][]byte
}

func NewReader(r io.Reader) *Reader {
	return &Reader{r: r, partial: map[byte][]byte{}}
}

// Next blocks until a complete message is available.
func (rd *Reader) Next() (Message, error) {
	for {
		f, err := ReadFrame(rd.r)
		if err != nil {
			return Message{}, err
		}
		if f.Flags&FlagFirst != 0 {
			rd.partial[f.Channel] = nil
		}
		buf := append(rd.partial[f.Channel], f.Payload...)
		if f.Flags&FlagLast == 0 {
			rd.partial[f.Channel] = buf
			continue
		}
		delete(rd.partial, f.Channel)
		return Message{
			Channel:   f.Channel,
			Control:   f.Flags&FlagControl != 0,
			Encrypted: f.Flags&FlagEncrypted != 0,
			Payload:   buf,
		}, nil
	}
}

// WriteMessage splits payload into frames of at most MaxFragment bytes.
func WriteMessage(w io.Writer, m Message) error {
	var flags byte
	if m.Control {
		flags |= FlagControl
	}
	if m.Encrypted {
		flags |= FlagEncrypted
	}
	p := m.Payload
	if len(p) <= MaxFragment {
		return WriteFrame(w, Frame{Channel: m.Channel, Flags: flags | FlagFirst | FlagLast, Payload: p})
	}
	total := uint32(len(p))
	for i := 0; len(p) > 0; i++ {
		n := min(len(p), MaxFragment)
		f := Frame{Channel: m.Channel, Flags: flags, Payload: p[:n]}
		if i == 0 {
			f.Flags |= FlagFirst
			f.TotalSize = total
		}
		if n == len(p) {
			f.Flags |= FlagLast
		}
		if err := WriteFrame(w, f); err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}
