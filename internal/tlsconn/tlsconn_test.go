package tlsconn

import (
	"bytes"
	"crypto/tls"
	"net"
	"testing"
)

// serverConfig reuses the embedded head unit cert as a throwaway server cert.
func serverConfig(t *testing.T) *tls.Config {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}}
}

func TestHandshakeAndRecords(t *testing.T) {
	a, b := net.Pipe()
	srv := tls.Server(b, serverConfig(t))
	go srv.Handshake()

	sess, err := Handshake(
		func(p []byte) error { _, err := a.Write(p); return err },
		func() ([]byte, error) {
			buf := make([]byte, 16*1024)
			n, err := a.Read(buf)
			return buf[:n], err
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	// client -> server: Encrypt, deliver the records, server reads plaintext
	rec, err := sess.Encrypt([]byte("hello phone"))
	if err != nil {
		t.Fatal(err)
	}
	go a.Write(rec)
	got := make([]byte, 64)
	n, err := srv.Read(got)
	if err != nil || string(got[:n]) != "hello phone" {
		t.Fatalf("server got %q, %v", got[:n], err)
	}

	// server -> client: capture server's records, Decrypt on the client
	go srv.Write([]byte("hello head unit"))
	buf := make([]byte, 16*1024)
	n, err = a.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := sess.Decrypt(buf[:n])
	if err != nil || !bytes.Equal(plain, []byte("hello head unit")) {
		t.Fatalf("client got %q, %v", plain, err)
	}
}
