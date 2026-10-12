package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"navix-moto/internal/aap"
	"navix-moto/internal/channels"
	"navix-moto/internal/tlsconn"
	"navix-moto/internal/transport"
)

func main() {
	phone := flag.String("phone", "", "phone IP address (required)")
	port := flag.Int("port", 5277, "headunit server port")
	outFile := flag.String("out", "out.h264", "file to write raw H.264 video to (empty to disable)")
	play := flag.Bool("play", false, "pipe video into ffplay for a live view")
	flag.Parse()
	if *phone == "" {
		log.Fatal("usage: navix -phone <ip>")
	}

	addr := net.JoinHostPort(*phone, fmt.Sprint(*port))
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		log.Fatalf("connect %s: %v", addr, err)
	}
	defer conn.Close()
	log.Printf("connected to %s", addr)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Printf("interrupted, closing")
		conn.Close()
	}()

	r := transport.NewReader(&loggingReader{conn})
	w := &loggingWriter{conn}

	req := transport.Message{Channel: aap.ChannelControl, Payload: aap.VersionRequest()}
	if err := transport.WriteMessage(w, req); err != nil {
		log.Fatalf("send version request: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	msg, err := r.Next()
	if err != nil {
		log.Fatalf("read version response: %v", err)
	}
	resp, err := aap.ParseVersionResponse(msg.Payload)
	if err != nil {
		log.Fatalf("parse version response: %v", err)
	}
	log.Printf("version response: %d.%d status=%d", resp.Major, resp.Minor, resp.Status)
	conn.SetReadDeadline(time.Time{})

	// Milestone 2: TLS handshake carried in SSL handshake messages on channel 0.
	sess, err := tlsconn.Handshake(
		func(b []byte) error {
			return transport.WriteMessage(w, transport.Message{
				Channel: aap.ChannelControl,
				Payload: aap.WithID(aap.MsgSSLHandshake, b),
			})
		},
		func() ([]byte, error) {
			for {
				m, err := r.Next()
				if err != nil {
					return nil, err
				}
				id, body, err := aap.SplitID(m.Payload)
				if err != nil || m.Channel != aap.ChannelControl || id != aap.MsgSSLHandshake {
					log.Printf("unexpected message during handshake: ch=%d % x", m.Channel, m.Payload)
					continue
				}
				return body, nil
			}
		},
	)
	if err != nil {
		log.Fatalf("tls handshake: %v", err)
	}
	st := sess.ConnectionState()
	log.Printf("tls handshake complete: version=%#x cipher=%#x", st.Version, st.CipherSuite)

	if err := transport.WriteMessage(w, transport.Message{
		Channel: aap.ChannelControl,
		Payload: aap.WithID(aap.MsgAuthComplete, aap.AuthCompleteOK),
	}); err != nil {
		log.Fatalf("send auth complete: %v", err)
	}

	// From here on, messages are encrypted. Dispatch each one to the session.
	var wmu sync.Mutex
	session := channels.NewSession(func(ch byte, control bool, payload []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		ct, err := sess.Encrypt(payload)
		if err != nil {
			return err
		}
		return transport.WriteMessage(w, transport.Message{Channel: ch, Control: control, Encrypted: true, Payload: ct})
	})

	conn.SetReadDeadline(time.Time{})
	sink, closeSink, err := openVideoSink(*outFile, *play)
	if err != nil {
		log.Fatalf("video sink: %v", err)
	}
	defer closeSink()
	session.VideoSink = sink
	for {
		m, err := r.Next()
		if err != nil {
			log.Printf("read: %v", err)
			return
		}
		payload := m.Payload
		if m.Encrypted {
			if payload, err = sess.Decrypt(payload); err != nil {
				log.Printf("decrypt: %v", err)
				return
			}
		}
		if id, body, err := aap.SplitID(payload); err == nil {
			log.Printf("message ch=%d id=%#04x len=%d body=%s", m.Channel, id, len(body), hexPreview(body))
		}
		if err := session.Handle(m.Channel, m.Control, payload); err != nil {
			if errors.Is(err, channels.ErrShutdown) {
				log.Printf("phone requested shutdown")
				return
			}
			log.Printf("handle: %v", err)
			return
		}
	}
}

func hexPreview(b []byte) string {
	const max = 48
	if len(b) > max {
		return hex.EncodeToString(b[:max]) + "..."
	}
	return hex.EncodeToString(b)
}

// loggingReader/Writer dump raw bytes as hex so every frame is visible.
type loggingReader struct{ c net.Conn }

func (l *loggingReader) Read(p []byte) (int, error) {
	n, err := l.c.Read(p)
	if n > 0 {
		log.Printf("<- %s", hex.EncodeToString(p[:n]))
	}
	return n, err
}

type loggingWriter struct{ c net.Conn }

func (l *loggingWriter) Write(p []byte) (int, error) {
	log.Printf("-> %s", hex.EncodeToString(p))
	return l.c.Write(p)
}

// openVideoSink returns a writer that fans raw H.264 out to a file and/or ffplay.
func openVideoSink(path string, play bool) (io.Writer, func(), error) {
	var writers []io.Writer
	var closers []func()
	if path != "" {
		f, err := os.Create(path)
		if err != nil {
			return nil, nil, err
		}
		writers = append(writers, f)
		closers = append(closers, func() { f.Close() })
	}
	if play {
		p := &player{}
		writers = append(writers, p)
		closers = append(closers, p.close)
	}
	if len(writers) == 0 {
		return io.Discard, func() {}, nil
	}
	return io.MultiWriter(writers...), func() {
		for _, c := range closers {
			c()
		}
	}, nil
}

// player feeds ffplay. It holds data back until the first keyframe has
// arrived, then starts ffplay and hands it the whole buffer (codec config and
// IDR) at once, so ffplay's stream probing sees a complete picture.
type player struct {
	pending []byte
	cmd     *exec.Cmd
	in      io.WriteCloser
}

func (p *player) Write(b []byte) (int, error) {
	if p.in == nil {
		p.pending = append(p.pending, b...)
		if !hasIDR(p.pending) {
			return len(b), nil
		}
		if err := p.start(); err != nil {
			return 0, err
		}
		_, err := p.in.Write(p.pending)
		p.pending = nil
		return len(b), err
	}
	return p.in.Write(b)
}

func (p *player) start() error {
	cmd := exec.Command("ffplay", "-loglevel", "warning", "-f", "h264",
		"-probesize", "65536", "-analyzeduration", "200000",
		"-flags", "low_delay", "-framedrop", "-i", "-")
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffplay: %w", err)
	}
	log.Printf("ffplay started; first %d bytes: % x", min(len(p.pending), 16), p.pending[:min(len(p.pending), 16)])
	p.cmd, p.in = cmd, in
	return nil
}

func (p *player) close() {
	if p.in != nil {
		p.in.Close()
		p.cmd.Wait()
	}
}

// hasIDR reports whether b contains an Annex B IDR slice (NAL type 5).
func hasIDR(b []byte) bool {
	for i := 0; i+4 < len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 && b[i+3]&0x1f == 5 {
			return true
		}
	}
	return false
}
