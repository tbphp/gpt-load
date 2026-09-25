package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	xproxy "golang.org/x/net/proxy"

	"gpt-load/internal/platform/config"
)

const (
	liveDataQueueSize = 64
	liveDataMaxBytes  = 256 << 10
)

type liveMediaSession struct {
	client          *webrtc.PeerConnection
	upstream        *webrtc.PeerConnection
	toClient        *webrtc.TrackLocalStaticRTP
	toUpstream      *webrtc.TrackLocalStaticRTP
	upstreamData    *webrtc.DataChannel
	clientData      *webrtc.DataChannel
	toClientData    chan webrtc.DataChannelMessage
	toUpstreamData  chan webrtc.DataChannelMessage
	clientReady     chan struct{}
	upstreamReady   chan struct{}
	done            chan struct{}
	closeOnce       sync.Once
	clientConnected atomic.Bool
	mu              sync.Mutex
	onClose         func(string)
	closeReason     string
	upstreamOffer   string
	proxyDialer     xproxy.ContextDialer
	tunnels         []*tcpCandidateTunnel
}

func newLiveMediaSession(ctx context.Context, clientOffer string, settings config.CodexLiveConfig, proxyURL string) (*liveMediaSession, error) {
	api, err := liveMediaAPI(settings)
	if err != nil {
		return nil, err
	}
	dialer, mode, err := proxyutil.BuildDialer(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("configure Codex live media proxy: %w", err)
	}
	var proxyDialer xproxy.ContextDialer
	upstreamAPI := api
	if mode == proxyutil.ModeProxy {
		var ok bool
		proxyDialer, ok = dialer.(xproxy.ContextDialer)
		if !ok {
			return nil, errors.New("Codex live media proxy does not support cancellation")
		}
		upstreamAPI, err = liveMediaAPIWithNetwork(settings, true)
		if err != nil {
			return nil, err
		}
	}
	configuration := webrtc.Configuration{ICEServers: make([]webrtc.ICEServer, 0, len(settings.ICEServers))}
	for _, server := range settings.ICEServers {
		configuration.ICEServers = append(configuration.ICEServers, webrtc.ICEServer{
			URLs: append([]string(nil), server.URLs...), Username: server.Username, Credential: server.Credential,
		})
	}
	client, err := api.NewPeerConnection(configuration)
	if err != nil {
		return nil, err
	}
	upstreamConfiguration := configuration
	if proxyDialer != nil {
		upstreamConfiguration.ICEServers = nil
	}
	upstream, err := upstreamAPI.NewPeerConnection(upstreamConfiguration)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	session := &liveMediaSession{
		client: client, upstream: upstream, proxyDialer: proxyDialer,
		toClientData:   make(chan webrtc.DataChannelMessage, liveDataQueueSize),
		toUpstreamData: make(chan webrtc.DataChannelMessage, liveDataQueueSize),
		clientReady:    make(chan struct{}), upstreamReady: make(chan struct{}), done: make(chan struct{}),
	}
	complete := false
	defer func() {
		if !complete {
			_ = session.Close()
		}
	}()
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1"}
	session.toClient, err = webrtc.NewTrackLocalStaticRTP(codec, "codex-audio", "gpt-load")
	if err != nil {
		return nil, err
	}
	session.toUpstream, err = webrtc.NewTrackLocalStaticRTP(codec, "codex-audio", "gpt-load")
	if err != nil {
		return nil, err
	}
	clientSender, err := client.AddTrack(session.toClient)
	if err != nil {
		return nil, err
	}
	upstreamSender, err := upstream.AddTrack(session.toUpstream)
	if err != nil {
		return nil, err
	}
	go session.drainRTCP(clientSender)
	go session.drainRTCP(upstreamSender)
	client.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		go session.relayAudio(track, session.toUpstream)
	})
	upstream.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) { go session.relayAudio(track, session.toClient) })
	client.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		if state == webrtc.ICEConnectionStateConnected || state == webrtc.ICEConnectionStateCompleted {
			session.clientConnected.Store(true)
		}
		if state == webrtc.ICEConnectionStateFailed || state == webrtc.ICEConnectionStateClosed {
			select {
			case <-session.done:
				return
			default:
			}
			session.endClientMedia()
		}
	})
	upstream.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		if state == webrtc.ICEConnectionStateFailed || state == webrtc.ICEConnectionStateClosed {
			select {
			case <-session.done:
				return
			default:
			}
			_ = session.closeWithReason("upstream_media_closed")
		}
	})
	client.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			session.clientConnected.Store(true)
		}
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			session.endClientMedia()
		}
	})
	upstream.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			select {
			case <-session.done:
				return
			default:
			}
			_ = session.closeWithReason("upstream_media_closed")
		}
	})
	session.upstreamData, err = upstream.CreateDataChannel("oai-events", nil)
	if err != nil {
		return nil, err
	}
	session.upstreamData.OnOpen(func() { close(session.upstreamReady) })
	session.upstreamData.OnMessage(func(message webrtc.DataChannelMessage) {
		session.enqueue(session.toClientData, message)
	})
	client.OnDataChannel(func(data *webrtc.DataChannel) {
		if data.Label() != "oai-events" {
			_ = session.closeWithReason("media_error")
			return
		}
		session.mu.Lock()
		if session.clientData != nil {
			session.mu.Unlock()
			_ = session.closeWithReason("media_error")
			return
		}
		session.clientData = data
		session.mu.Unlock()
		data.OnOpen(func() { close(session.clientReady) })
		data.OnMessage(func(message webrtc.DataChannelMessage) { session.enqueue(session.toUpstreamData, message) })
	})
	go session.forwardData(session.toClientData, session.clientReady, func() *webrtc.DataChannel {
		session.mu.Lock()
		defer session.mu.Unlock()
		return session.clientData
	})
	go session.forwardData(session.toUpstreamData, session.upstreamReady, func() *webrtc.DataChannel { return session.upstreamData })
	if err := client.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: clientOffer}); err != nil {
		return nil, fmt.Errorf("accept client audio offer: %w", err)
	}
	offer, err := upstream.CreateOffer(nil)
	if err != nil {
		return nil, err
	}
	if err := upstream.SetLocalDescription(offer); err != nil {
		return nil, err
	}
	if err := awaitLiveICE(ctx, upstream); err != nil {
		return nil, err
	}
	session.upstreamOffer = upstream.LocalDescription().SDP
	complete = true
	return session, nil
}

func liveMediaAPI(settings config.CodexLiveConfig) (*webrtc.API, error) {
	return liveMediaAPIWithNetwork(settings, false)
}

func liveMediaAPIWithNetwork(settings config.CodexLiveConfig, loopbackOnly bool) (*webrtc.API, error) {
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
			SDPFmtpLine: "minptime=10;useinbandfec=1",
		}, PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}
	interceptors := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, interceptors); err != nil {
		return nil, err
	}
	engine := webrtc.SettingEngine{}
	if loopbackOnly {
		engine.SetNetworkTypes([]webrtc.NetworkType{
			webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6,
			webrtc.NetworkTypeTCP4, webrtc.NetworkTypeTCP6,
		})
		engine.SetIncludeLoopbackCandidate(true)
		engine.SetIPFilter(func(ip net.IP) bool { return ip != nil && ip.IsLoopback() })
	} else {
		engine.SetIncludeLoopbackCandidate(true)
		if settings.UDPPortMin > 0 {
			if err := engine.SetEphemeralUDPPortRange(settings.UDPPortMin, settings.UDPPortMax); err != nil {
				return nil, err
			}
		}
		if settings.PublicIP != "" {
			engine.SetNAT1To1IPs([]string{settings.PublicIP}, webrtc.ICECandidateTypeHost)
		}
	}
	return webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine), webrtc.WithInterceptorRegistry(interceptors), webrtc.WithSettingEngine(engine)), nil
}

func awaitLiveICE(ctx context.Context, peer *webrtc.PeerConnection) error {
	select {
	case <-webrtc.GatheringCompletePromise(peer):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (session *liveMediaSession) AcceptAnswer(ctx context.Context, upstreamAnswer string) (string, error) {
	if session == nil {
		return "", errors.New("live media session unavailable")
	}
	if session.proxyDialer != nil {
		rewritten, tunnels, err := prepareProxiedUpstreamAnswer(upstreamAnswer, session.upstreamOffer, session.proxyDialer)
		if err != nil {
			return "", fmt.Errorf("proxy Codex live media: %w", err)
		}
		session.mu.Lock()
		select {
		case <-session.done:
			session.mu.Unlock()
			_ = closeCandidateTunnels(tunnels)
			return "", errors.New("Codex live media session closed")
		default:
		}
		session.tunnels = tunnels
		session.mu.Unlock()
		upstreamAnswer = rewritten
	}
	if err := session.upstream.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: upstreamAnswer}); err != nil {
		return "", fmt.Errorf("accept upstream audio answer: %w", err)
	}
	answer, err := session.client.CreateAnswer(nil)
	if err != nil {
		return "", err
	}
	if err := session.client.SetLocalDescription(answer); err != nil {
		return "", err
	}
	if err := awaitLiveICE(ctx, session.client); err != nil {
		return "", err
	}
	return session.client.LocalDescription().SDP, nil
}

func (session *liveMediaSession) enqueue(queue chan<- webrtc.DataChannelMessage, message webrtc.DataChannelMessage) {
	if len(message.Data) > liveDataMaxBytes {
		_ = session.closeWithReason("media_error")
		return
	}
	copyMessage := webrtc.DataChannelMessage{Data: append([]byte(nil), message.Data...), IsString: message.IsString}
	select {
	case queue <- copyMessage:
	case <-session.done:
	default:
		_ = session.closeWithReason("media_error")
	}
}

func (session *liveMediaSession) forwardData(queue <-chan webrtc.DataChannelMessage, ready <-chan struct{}, destination func() *webrtc.DataChannel) {
	select {
	case <-ready:
	case <-session.done:
		return
	}
	for {
		select {
		case message := <-queue:
			data := destination()
			if data == nil {
				_ = session.closeWithReason("media_error")
				return
			}
			var err error
			if message.IsString {
				err = data.SendText(string(message.Data))
			} else {
				err = data.Send(message.Data)
			}
			if err != nil {
				_ = session.closeWithReason("media_error")
				return
			}
		case <-session.done:
			return
		}
	}
}

func (session *liveMediaSession) relayAudio(source *webrtc.TrackRemote, destination *webrtc.TrackLocalStaticRTP) {
	for {
		packet, _, err := source.ReadRTP()
		if err != nil {
			return
		}
		packet.Extension = false
		packet.ExtensionProfile = 0
		packet.Extensions = nil
		if destination.WriteRTP(packet) != nil {
			return
		}
	}
}

func (session *liveMediaSession) drainRTCP(sender *webrtc.RTPSender) {
	for {
		if _, _, err := sender.ReadRTCP(); err != nil && !errors.Is(err, io.EOF) {
			return
		} else if err != nil {
			return
		}
	}
}

func (session *liveMediaSession) endClientMedia() {
	select {
	case <-session.done:
		return
	default:
	}
	if session.clientConnected.Load() {
		_ = session.closeWithReason("client_media_ended")
		return
	}
	_ = session.closeWithReason("media_closed")
}

func (session *liveMediaSession) OnClose(callback func(string)) {
	session.mu.Lock()
	reason := session.closeReason
	select {
	case <-session.done:
	default:
		session.onClose = callback
		callback = nil
	}
	session.mu.Unlock()
	if callback != nil {
		callback(reason)
	}
}

func (session *liveMediaSession) Close() error {
	return session.closeWithReason("media_closed")
}

func (session *liveMediaSession) closeWithReason(reason string) error {
	if session == nil {
		return nil
	}
	var result error
	session.closeOnce.Do(func() {
		session.mu.Lock()
		session.closeReason = reason
		close(session.done)
		session.mu.Unlock()
		if err := session.client.Close(); err != nil {
			result = err
		}
		if err := session.upstream.Close(); err != nil && result == nil {
			result = err
		}
		session.mu.Lock()
		tunnels := session.tunnels
		session.tunnels = nil
		callback := session.onClose
		session.onClose = nil
		session.mu.Unlock()
		if err := closeCandidateTunnels(tunnels); err != nil && result == nil {
			result = err
		}
		if callback != nil {
			callback(reason)
		}
	})
	return result
}
