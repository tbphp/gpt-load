package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"gpt-load/internal/platform/config"
)

func TestCodexLiveMediaRelaysDataChannelBothWays(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	api, err := liveMediaAPI(config.CodexLiveConfig{})
	if err != nil {
		t.Fatal(err)
	}
	client, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	clientTrack, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "client")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.AddTrack(clientTrack); err != nil {
		t.Fatal(err)
	}
	clientData, err := client.CreateDataChannel("oai-events", nil)
	if err != nil {
		t.Fatal(err)
	}
	clientReady := make(chan struct{})
	clientReceived := make(chan string, 1)
	clientData.OnOpen(func() { close(clientReady) })
	clientData.OnMessage(func(message webrtc.DataChannelMessage) { clientReceived <- string(message.Data) })
	clientAudio := make(chan []byte, 1)
	client.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		packet, _, readErr := track.ReadRTP()
		if readErr == nil {
			clientAudio <- packet.Payload
		}
	})
	offer, err := client.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	if err := awaitLiveICE(ctx, client); err != nil {
		t.Fatal(err)
	}
	relay, err := newLiveMediaSession(ctx, client.LocalDescription().SDP, config.CodexLiveConfig{}, "direct")
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	upstream, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	upstreamReceived := make(chan string, 1)
	upstreamReady := make(chan *webrtc.DataChannel, 1)
	upstreamAudio := make(chan []byte, 1)
	upstream.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		packet, _, readErr := track.ReadRTP()
		if readErr == nil {
			upstreamAudio <- packet.Payload
		}
	})
	upstream.OnDataChannel(func(data *webrtc.DataChannel) {
		data.OnOpen(func() { upstreamReady <- data })
		data.OnMessage(func(message webrtc.DataChannelMessage) { upstreamReceived <- string(message.Data) })
	})
	if err := upstream.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: relay.upstreamOffer}); err != nil {
		t.Fatal(err)
	}
	upstreamTrack, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "upstream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upstream.AddTrack(upstreamTrack); err != nil {
		t.Fatal(err)
	}
	upstreamAnswer, err := upstream.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := upstream.SetLocalDescription(upstreamAnswer); err != nil {
		t.Fatal(err)
	}
	if err := awaitLiveICE(ctx, upstream); err != nil {
		t.Fatal(err)
	}
	answer, err := relay.AcceptAnswer(ctx, upstream.LocalDescription().SDP)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer}); err != nil {
		t.Fatal(err)
	}
	var upstreamData *webrtc.DataChannel
	select {
	case <-clientReady:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case upstreamData = <-upstreamReady:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := clientData.SendText("client-event"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-upstreamReceived:
		if got != "client-event" {
			t.Fatalf("upstream event = %q", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := upstreamData.SendText("upstream-event"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-clientReceived:
		if got != "upstream-event" {
			t.Fatalf("client event = %q", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	clientPacket := &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: 1, Timestamp: 480}, Payload: []byte{0xF8, 0xFF, 0xFE}}
	if err := clientTrack.WriteRTP(clientPacket); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-upstreamAudio:
		if string(got) != string(clientPacket.Payload) {
			t.Fatalf("upstream audio = %v", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	upstreamPacket := &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: 2, Timestamp: 960}, Payload: []byte{0xF8, 0xFF, 0xFD}}
	if err := upstreamTrack.WriteRTP(upstreamPacket); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-clientAudio:
		if string(got) != string(upstreamPacket.Payload) {
			t.Fatalf("client audio = %v", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
