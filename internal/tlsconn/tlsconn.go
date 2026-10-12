// Package tlsconn runs crypto/tls over the Android Auto transport.
//
// During the handshake, TLS bytes travel inside "SSL handshake" messages on
// the control channel, so the adapter hands them to caller-supplied send/recv
// functions. After the handshake, frames carry bare TLS records, so the
// adapter switches to in-memory buffers: Encrypt turns plaintext into records
// to put in a frame, Decrypt turns records from a frame back into plaintext.
package tlsconn

import (
	"bytes"
	"crypto/tls"
	_ "embed"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

//go:embed certs/headunit.crt
var certPEM []byte

//go:embed certs/headunit.key
var keyPEM []byte

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "no data buffered" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// pipeConn is the net.Conn handed to crypto/tls.
type pipeConn struct {
	mu       sync.Mutex
	send     func([]byte) error     // handshake mode: emit TLS bytes
	recv     func() ([]byte, error) // handshake mode: next chunk of TLS bytes
	pending  bytes.Buffer           // leftover from recv, or inbound records after the handshake
	out      bytes.Buffer           // outbound records after the handshake
	buffered bool
}

func (c *pipeConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending.Len() == 0 {
		if c.buffered {
			return 0, timeoutErr{}
		}
		c.mu.Unlock()
		b, err := c.recv()
		c.mu.Lock()
		if err != nil {
			return 0, err
		}
		c.pending.Write(b)
	}
	return c.pending.Read(p)
}

func (c *pipeConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	buffered := c.buffered
	if buffered {
		c.out.Write(p)
	}
	c.mu.Unlock()
	if buffered {
		return len(p), nil
	}
	if err := c.send(append([]byte(nil), p...)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *pipeConn) Close() error                     { return nil }
func (c *pipeConn) LocalAddr() net.Addr              { return addr{} }
func (c *pipeConn) RemoteAddr() net.Addr             { return addr{} }
func (c *pipeConn) SetDeadline(time.Time) error      { return nil }
func (c *pipeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *pipeConn) SetWriteDeadline(time.Time) error { return nil }

type addr struct{}

func (addr) Network() string { return "aap" }
func (addr) String() string  { return "aap" }

// Session is an established TLS session over the Android Auto transport.
type Session struct {
	pc   *pipeConn
	conn *tls.Conn
}

// Handshake performs the TLS client handshake. send transmits TLS bytes to
// the phone (wrapped in an SSL handshake message by the caller); recv returns
// the TLS bytes of the next SSL handshake message from the phone.
func Handshake(send func([]byte) error, recv func() ([]byte, error)) (*Session, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	pc := &pipeConn{send: send, recv: recv}
	conn := tls.Client(pc, &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true, // the phone's certificate is not meaningfully verifiable
		MinVersion:         tls.VersionTLS12,
	})
	if err := conn.Handshake(); err != nil {
		return nil, err
	}
	pc.mu.Lock()
	pc.buffered = true
	pc.mu.Unlock()
	return &Session{pc: pc, conn: conn}, nil
}

// ConnectionState exposes the negotiated TLS parameters for logging.
func (s *Session) ConnectionState() tls.ConnectionState { return s.conn.ConnectionState() }

// Encrypt returns the TLS record bytes for plaintext.
func (s *Session) Encrypt(plaintext []byte) ([]byte, error) {
	if _, err := s.conn.Write(plaintext); err != nil {
		return nil, err
	}
	s.pc.mu.Lock()
	defer s.pc.mu.Unlock()
	out := append([]byte(nil), s.pc.out.Bytes()...)
	s.pc.out.Reset()
	return out, nil
}

// Decrypt feeds TLS record bytes from one frame and returns all plaintext
// they contain.
func (s *Session) Decrypt(records []byte) ([]byte, error) {
	s.pc.mu.Lock()
	s.pc.pending.Write(records)
	s.pc.mu.Unlock()

	var plain bytes.Buffer
	buf := make([]byte, 16*1024)
	for {
		n, err := s.conn.Read(buf)
		plain.Write(buf[:n])
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return plain.Bytes(), nil // all buffered records consumed
			}
			if err == io.EOF {
				return plain.Bytes(), nil
			}
			return plain.Bytes(), err
		}
	}
}
