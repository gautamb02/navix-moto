package transport

import (
	"bytes"
	"testing"
)

func TestSingleFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	in := Message{Channel: 0, Payload: []byte{0, 1, 0, 1, 0, 1}}
	if err := WriteMessage(&buf, in); err != nil {
		t.Fatal(err)
	}
	// channel 0, flags first|last = 0x03, length 6
	want := []byte{0, 0x03, 0, 6, 0, 1, 0, 1, 0, 1}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("got % x, want % x", buf.Bytes(), want)
	}
	out, err := NewReader(&buf).Next()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Payload, in.Payload) {
		t.Fatalf("round trip mismatch: %+v", out)
	}
}

func TestFragmentedRoundTrip(t *testing.T) {
	payload := make([]byte, MaxFragment*2+123)
	for i := range payload {
		payload[i] = byte(i)
	}
	var buf bytes.Buffer
	if err := WriteMessage(&buf, Message{Channel: 3, Encrypted: true, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	// first frame: first flag + encrypted; length counts the fragment only,
	// and the 4-byte total size follows the header in addition
	b := buf.Bytes()
	if b[1] != FlagFirst|FlagEncrypted {
		t.Fatalf("first flags = %#x", b[1])
	}
	if got := int(b[2])<<8 | int(b[3]); got != MaxFragment {
		t.Fatalf("first length = %d", got)
	}
	if total := int(b[4])<<24 | int(b[5])<<16 | int(b[6])<<8 | int(b[7]); total != len(payload) {
		t.Fatalf("total size = %d, want %d", total, len(payload))
	}
	out, err := NewReader(&buf).Next()
	if err != nil {
		t.Fatal(err)
	}
	if out.Channel != 3 || !out.Encrypted || !bytes.Equal(out.Payload, payload) {
		t.Fatal("fragmented round trip mismatch")
	}
}
