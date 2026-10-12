package aap

import (
	"testing"

	"google.golang.org/protobuf/proto"

	pb "navix-moto/proto/pb/aap"
)

func TestServiceDiscoveryResponse(t *testing.T) {
	payload, err := ServiceDiscoveryResponse()
	if err != nil {
		t.Fatal(err)
	}
	id, body, err := SplitID(payload)
	if err != nil || id != MsgServiceDiscoveryResponse {
		t.Fatalf("id=%#x err=%v", id, err)
	}
	var resp pb.ServiceDiscoveryResponse
	if err := proto.Unmarshal(body, &resp); err != nil { // fails if a required field is missing
		t.Fatal(err)
	}
	if got := resp.GetHeadunitInfo().GetVehicleType(); got != VehicleTypeMotorcycle {
		t.Fatalf("vehicle type = %d", got)
	}
	var video, audio bool
	for _, s := range resp.Services {
		if s.MediaSinkService != nil && s.GetId() == uint32(ChannelVideo) {
			video = true
		}
		if s.MediaSinkService != nil && s.GetId() == uint32(ChannelAudioSystem) {
			audio = true
		}
		if s.MediaSourceService != nil {
			t.Fatal("microphone service must not be announced")
		}
	}
	if !video || !audio {
		t.Fatalf("video=%v audio=%v", video, audio)
	}
}
