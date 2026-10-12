// Package channels dispatches decrypted Android Auto messages to per-channel
// handlers and sends the replies.
package channels

import (
	"errors"
	"fmt"
	"io"
	"log"
	"sync"

	"google.golang.org/protobuf/proto"

	"navix-moto/internal/aap"
	pb "navix-moto/proto/pb/aap"
)

// ErrShutdown is returned from Handle when the phone asked us to disconnect.
var ErrShutdown = errors.New("phone requested shutdown")

// Sender transmits one decrypted payload (message ID + body) on a channel.
// control sets the frame's control flag.
type Sender func(ch byte, control bool, payload []byte) error

// Message IDs on service channels (scoped by channel, not global).
const (
	msgMediaSetupRequest uint16 = 0x8000
	msgSensorStart       uint16 = 0x8001
	msgSensorStartResp   uint16 = 0x8002
	msgSensorEvent       uint16 = 0x8003
	msgInputBindingReq   uint16 = 0x8002
	msgInputBindingResp  uint16 = 0x8003

	// Media channels (video, audio sinks).
	msgMediaData        uint16 = 0x0000 // 8-byte timestamp + payload
	msgMediaCodecConfig uint16 = 0x0001 // payload only (H.264 SPS/PPS)
	msgMediaStart       uint16 = 0x8001
	msgMediaStop        uint16 = 0x8002
	msgMediaConfig      uint16 = 0x8003
	msgMediaAck         uint16 = 0x8004
	msgVideoFocusReq    uint16 = 0x8007
	msgVideoFocusNotify uint16 = 0x8008

	maxUnackedVideo = 12
	maxUnackedAudio = 30
)

// Session holds per-connection state and replies to the phone's requests.
type Session struct {
	send Sender

	// VideoSink receives raw H.264 (Annex B) from the video channel. May be nil.
	VideoSink io.Writer

	mu        sync.Mutex
	sessionID map[byte]int32 // media session ID per channel, from MediaStart
	frames    int
	bytes     int
}

func NewSession(send Sender) *Session {
	return &Session{send: send, sessionID: map[byte]int32{}}
}

func isMedia(ch byte) bool { return ch == aap.ChannelVideo || ch == aap.ChannelAudioSystem }

// Handle processes one decrypted message. Unknown messages are logged and
// ignored; an error means the connection should be closed.
func (s *Session) Handle(ch byte, control bool, payload []byte) error {
	id, body, err := aap.SplitID(payload)
	if err != nil {
		return err
	}
	switch {
	case ch == aap.ChannelControl:
		return s.handleControl(id, body)
	case id == aap.MsgChannelOpenRequest:
		return s.channelOpen(ch, body)
	case ch == aap.ChannelSensor && id == msgSensorStart:
		return s.sensorStart(ch, body)
	case ch == aap.ChannelInput && id == msgInputBindingReq:
		return s.reply(ch, msgInputBindingResp, &pb.BindingResponse{Status: pb.MessageStatus_STATUS_SUCCESS.Enum()})
	case isMedia(ch) && !control:
		return s.handleMedia(ch, id, body)
	}
	log.Printf("ch=%d unhandled id=%#04x body=%x", ch, id, body)
	return nil
}

func (s *Session) handleControl(id uint16, body []byte) error {
	switch id {
	case aap.MsgServiceDiscoveryRequest:
		resp, err := aap.ServiceDiscoveryResponse()
		if err != nil {
			return err
		}
		return s.send(aap.ChannelControl, false, resp)
	case aap.MsgPingRequest:
		var req pb.PingRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			return fmt.Errorf("ping request: %w", err)
		}
		// Echo the phone's token; our clock has a different origin.
		return s.reply(aap.ChannelControl, aap.MsgPingResponse, &pb.PingResponse{Timestamp: req.Timestamp})
	case aap.MsgNavFocusRequest:
		return s.reply(aap.ChannelControl, aap.MsgNavFocusResponse,
			&pb.NavFocusNotification{FocusType: pb.NavFocusType_NAV_FOCUS_PROJECTED.Enum()})
	case aap.MsgAudioFocusRequest:
		var req pb.AudioFocusRequestNotification
		if err := proto.Unmarshal(body, &req); err != nil {
			return fmt.Errorf("audio focus request: %w", err)
		}
		return s.reply(aap.ChannelControl, aap.MsgAudioFocusResponse,
			&pb.AudioFocusNotification{FocusState: audioFocusState(req.GetRequest()).Enum()})
	case aap.MsgShutdownRequest:
		_ = s.reply(aap.ChannelControl, aap.MsgShutdownResponse, &pb.ByeByeResponse{})
		return ErrShutdown
	case aap.MsgVoiceSession:
		return nil
	}
	log.Printf("control: unhandled id=%#04x body=%x", id, body)
	return nil
}

// audioFocusState always grants focus: the phone must believe the head unit
// has it, or it keeps audio on the phone.
func audioFocusState(r pb.AudioFocusRequestNotification_AudioFocusRequestType) pb.AudioFocusNotification_AudioFocusStateType {
	switch r {
	case pb.AudioFocusRequestNotification_RELEASE:
		return pb.AudioFocusNotification_STATE_LOSS
	case pb.AudioFocusRequestNotification_GAIN_TRANSIENT:
		return pb.AudioFocusNotification_STATE_GAIN_TRANSIENT
	case pb.AudioFocusRequestNotification_GAIN_TRANSIENT_MAY_DUCK:
		return pb.AudioFocusNotification_STATE_GAIN_TRANSIENT_GUIDANCE_ONLY
	}
	return pb.AudioFocusNotification_STATE_GAIN
}

func (s *Session) channelOpen(ch byte, body []byte) error {
	var req pb.ChannelOpenRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("channel open request: %w", err)
	}
	log.Printf("channel open: ch=%d service_id=%d priority=%d", ch, req.GetServiceId(), req.GetPriority())
	if err := s.reply(ch, aap.MsgChannelOpenResponse, &pb.ChannelOpenResponse{Status: pb.MessageStatus_STATUS_SUCCESS.Enum()}); err != nil {
		return err
	}
	if ch == aap.ChannelSensor {
		return s.sendDrivingStatus()
	}
	return nil
}

func (s *Session) sensorStart(ch byte, body []byte) error {
	var req pb.SensorRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("sensor start request: %w", err)
	}
	log.Printf("sensor start: %v", req.GetType())
	if err := s.reply(ch, msgSensorStartResp, &pb.SensorResponse{Status: pb.MessageStatus_STATUS_SUCCESS.Enum()}); err != nil {
		return err
	}
	switch req.GetType() {
	case pb.SensorType_NIGHT:
		return s.sendNightMode(false)
	case pb.SensorType_DRIVING_STATUS:
		return s.sendDrivingStatus()
	}
	return nil
}

func (s *Session) sendDrivingStatus() error {
	return s.reply(aap.ChannelSensor, msgSensorEvent, &pb.SensorBatch{
		DrivingStatus: []*pb.SensorBatch_DrivingStatusData{
			{Status: proto.Int32(int32(pb.SensorBatch_DrivingStatusData_UNRESTRICTED))},
		},
	})
}

func (s *Session) sendNightMode(night bool) error {
	return s.reply(aap.ChannelSensor, msgSensorEvent, &pb.SensorBatch{
		NightMode: []*pb.SensorBatch_NightData{{IsNightMode: proto.Bool(night)}},
	})
}

// reply marshals m and sends it with the right control flag: messages with
// IDs 1..26 on service channels are "control type" and carry the flag.
func (s *Session) reply(ch byte, id uint16, m proto.Message) error {
	b, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	control := ch != aap.ChannelControl && id >= 1 && id <= 26
	return s.send(ch, control, aap.WithID(id, b))
}

func (s *Session) handleMedia(ch byte, id uint16, body []byte) error {
	switch id {
	case msgMediaSetupRequest:
		return s.mediaSetup(ch, body)
	case msgMediaStart:
		var req pb.Start
		if err := proto.Unmarshal(body, &req); err != nil {
			return fmt.Errorf("media start: %w", err)
		}
		s.mu.Lock()
		s.sessionID[ch] = req.GetSessionId()
		s.mu.Unlock()
		log.Printf("ch=%d media start: session=%d config_index=%d", ch, req.GetSessionId(), req.GetConfigurationIndex())
		return nil
	case msgMediaStop:
		log.Printf("ch=%d media stop", ch)
		return nil
	case msgVideoFocusReq:
		var req pb.VideoFocusRequestNotification
		if err := proto.Unmarshal(body, &req); err != nil {
			return fmt.Errorf("video focus request: %w", err)
		}
		log.Printf("ch=%d video focus request: mode=%v reason=%v", ch, req.GetMode(), req.GetReason())
		return nil
	case msgMediaData, msgMediaCodecConfig:
		return s.mediaData(ch, id, body)
	}
	log.Printf("ch=%d media: unhandled id=%#04x body=%x", ch, id, body)
	return nil
}

func (s *Session) mediaSetup(ch byte, body []byte) error {
	var req pb.MediaSetupRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("media setup: %w", err)
	}
	max := uint32(maxUnackedAudio)
	if ch == aap.ChannelVideo {
		max = maxUnackedVideo
	}
	log.Printf("ch=%d media setup: type=%d, replying ready (max_unacked=%d)", ch, req.GetType(), max)
	if err := s.reply(ch, msgMediaConfig, &pb.Config{
		Status:               pb.Config_STATUS_READY.Enum(),
		MaxUnacked:           proto.Uint32(max),
		ConfigurationIndices: []uint32{0},
	}); err != nil {
		return err
	}
	if ch == aap.ChannelVideo {
		return s.reply(ch, msgVideoFocusNotify, &pb.VideoFocusNotification{
			Mode:        pb.VideoFocusMode_VIDEO_FOCUS_PROJECTED.Enum(),
			Unsolicited: proto.Bool(true),
		})
	}
	// Audio sink: tell the phone it holds audio focus.
	return s.reply(aap.ChannelControl, aap.MsgAudioFocusResponse, &pb.AudioFocusNotification{
		FocusState:  pb.AudioFocusNotification_STATE_GAIN.Enum(),
		Unsolicited: proto.Bool(true),
	})
}

// mediaData handles one complete media message: forward video to the sink,
// then acknowledge it. The phone stops sending if too many are unacknowledged.
func (s *Session) mediaData(ch byte, id uint16, body []byte) error {
	data := body
	if id == msgMediaData {
		if len(body) < 8 {
			return fmt.Errorf("media data too short: %d bytes", len(body))
		}
		data = body[8:] // skip 64-bit timestamp
	}
	if ch == aap.ChannelVideo {
		s.mu.Lock()
		s.frames++
		s.bytes += len(data)
		n := s.frames
		s.mu.Unlock()
		if n == 1 || n%100 == 0 {
			log.Printf("video: message %d, %d bytes (id=%#04x)", n, len(data), id)
		}
		if s.VideoSink != nil {
			if _, err := s.VideoSink.Write(data); err != nil {
				log.Printf("video sink failed, disabling: %v", err)
				s.VideoSink = nil
			}
		}
	}
	s.mu.Lock()
	sid := s.sessionID[ch]
	s.mu.Unlock()
	return s.reply(ch, msgMediaAck, &pb.Ack{SessionId: proto.Int32(sid), Ack: proto.Uint32(1)})
}
