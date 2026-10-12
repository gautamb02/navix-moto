package main

import "testing"

func TestHasIDR(t *testing.T) {
	sps := []byte{0, 0, 0, 1, 0x67, 0x42, 0, 0x1f, 0, 0, 0, 1, 0x68, 0xce}
	if hasIDR(sps) {
		t.Fatal("SPS/PPS alone must not count as a keyframe")
	}
	idr := append(append([]byte{}, sps...), 0, 0, 0, 1, 0x65, 0xb8, 0x4f)
	if !hasIDR(idr) {
		t.Fatal("IDR slice not detected")
	}
	p := []byte{0, 0, 0, 1, 0x21, 0xe2, 0x05}
	if hasIDR(p) {
		t.Fatal("P slice must not count as a keyframe")
	}
}
