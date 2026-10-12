package aap

import (
	"google.golang.org/protobuf/proto"

	pb "navix-moto/proto/pb/aap"
)

// Control channel message IDs used after the handshake.
const (
	MsgServiceDiscoveryRequest  uint16 = 0x0005
	MsgServiceDiscoveryResponse uint16 = 0x0006
	MsgChannelOpenRequest       uint16 = 0x0007
	MsgChannelOpenResponse      uint16 = 0x0008
	MsgPingRequest              uint16 = 0x000b
	MsgPingResponse             uint16 = 0x000c
	MsgNavFocusRequest          uint16 = 0x000d
	MsgNavFocusResponse         uint16 = 0x000e
	MsgShutdownRequest          uint16 = 0x000f
	MsgShutdownResponse         uint16 = 0x0010
	MsgVoiceSession             uint16 = 0x0011
	MsgAudioFocusRequest        uint16 = 0x0012
	MsgAudioFocusResponse       uint16 = 0x0013
)

// Channel IDs, as used by Headunit Revived. The phone learns them from the
// discovery response, so they are our choice, but a known-good layout is safer.
const (
	ChannelSensor      byte = 1
	ChannelVideo       byte = 2
	ChannelInput       byte = 3
	ChannelAudioSystem byte = 5
)

// Video parameters advertised to the phone (smallest Android Auto size).
const (
	VideoWidth  = 800
	VideoHeight = 480
	VideoDPI    = 140
)

// VehicleTypeMotorcycle is Android Auto's VEHICLE_TYPE_MOTORCYCLE. Claiming it
// without announcing a microphone service makes the phone record with its own
// microphone; any other type without a mic aborts setup with "No audio/mic".
const VehicleTypeMotorcycle int32 = 3

// ServiceDiscoveryResponse describes this "car": video, touch input, driving
// status and night mode sensors, and the system-sound audio sink the phone
// insists on. It claims to be a motorcycle with no microphone.
func ServiceDiscoveryResponse() ([]byte, error) {
	const (
		make_   = "navix"
		model   = "moto"
		year    = "2026"
		vehicle = "navix-moto-0001"
	)
	resp := &pb.ServiceDiscoveryResponse{
		Services: []*pb.Service{
			{
				Id: proto.Uint32(uint32(ChannelSensor)),
				SensorSourceService: &pb.Service_SensorSourceService{Sensors: []*pb.Service_SensorSourceService_Sensor{
					{Type: pb.SensorType_DRIVING_STATUS.Enum()},
					{Type: pb.SensorType_NIGHT.Enum()},
				}},
			},
			{
				Id: proto.Uint32(uint32(ChannelVideo)),
				MediaSinkService: &pb.Service_MediaSinkService{
					AvailableType:        pb.MediaCodecType_MEDIA_CODEC_VIDEO_H264_BP.Enum(),
					AudioType:            pb.AudioStreamType_NONE.Enum(),
					AvailableWhileInCall: proto.Bool(true),
					VideoConfigs: []*pb.Service_MediaSinkService_VideoConfiguration{{
						CodecResolution:    pb.Service_MediaSinkService_VideoConfiguration__800x480.Enum(),
						FrameRate:          pb.Service_MediaSinkService_VideoConfiguration__30.Enum(),
						MarginWidth:        proto.Uint32(0),
						MarginHeight:       proto.Uint32(0),
						Density:            proto.Uint32(VideoDPI),
						PixelAspectRatioE4: proto.Uint32(10000),
						VideoCodecType:     pb.MediaCodecType_MEDIA_CODEC_VIDEO_H264_BP.Enum(),
					}},
				},
			},
			{
				Id: proto.Uint32(uint32(ChannelInput)),
				InputSourceService: &pb.Service_InputSourceService{
					Touchscreen: &pb.Service_InputSourceService_TouchConfig{
						Width:  proto.Uint32(VideoWidth),
						Height: proto.Uint32(VideoHeight),
					},
				},
			},
			{
				// 16 kHz mono is required: the phone accepts the SYSTEM sink only
				// with that config, and it ends the session if no audio sink exists.
				Id: proto.Uint32(uint32(ChannelAudioSystem)),
				MediaSinkService: &pb.Service_MediaSinkService{
					AvailableType: pb.MediaCodecType_MEDIA_CODEC_AUDIO_PCM.Enum(),
					AudioType:     pb.AudioStreamType_SYSTEM.Enum(),
					AudioConfigs: []*pb.AudioConfiguration{{
						SampleRate:       proto.Uint32(16000),
						NumberOfBits:     proto.Uint32(16),
						NumberOfChannels: proto.Uint32(1),
					}},
				},
			},
		},
		Make:                       proto.String(make_),
		Model:                      proto.String(model),
		Year:                       proto.String(year),
		VehicleId:                  proto.String(vehicle),
		DriverPosition:             pb.DriverPosition_DRIVER_POSITION_LEFT.Enum(),
		HeadUnitMake:               proto.String("navix"),
		HeadUnitModel:              proto.String("navix-moto"),
		HeadUnitSoftwareBuild:      proto.String("1"),
		HeadUnitSoftwareVersion:    proto.String("0.1.0"),
		CanPlayNativeMediaDuringVr: proto.Bool(false),
		DisplayName:                proto.String("navix-moto"),
		HeadunitInfo: &pb.HeadUnitInfo{
			Make:                    proto.String(make_),
			Model:                   proto.String(model),
			Year:                    proto.String(year),
			VehicleId:               proto.String(vehicle),
			HeadUnitMake:            proto.String("navix"),
			HeadUnitModel:           proto.String("navix-moto"),
			HeadUnitSoftwareBuild:   proto.String("1"),
			HeadUnitSoftwareVersion: proto.String("0.1.0"),
			VehicleType:             proto.Int32(VehicleTypeMotorcycle),
		},
	}
	b, err := proto.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return WithID(MsgServiceDiscoveryResponse, b), nil
}
