package gateway

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/pion/sdp/v3"
	"github.com/pion/stun/v3"
)

type liveProxyDialer struct{ dialed chan string }

func (d *liveProxyDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}

func (d *liveProxyDialer) DialContext(_ context.Context, _, address string) (net.Conn, error) {
	d.dialed <- address
	return nil, fmt.Errorf("test proxy blocked %s", address)
}

func TestCodexLiveProxyRewritesOnlySafeTCPCandidates(t *testing.T) {
	t.Parallel()
	dialer := &liveProxyDialer{dialed: make(chan string, 1)}
	answer := liveProxySDP("remote", "remote-password", []string{
		"1 1 udp 2130706431 20.42.0.10 3478 typ host",
		"2 1 tcp 1671430143 20.42.0.20 443 typ host tcptype passive",
	})
	rewritten, tunnels, err := prepareProxiedUpstreamAnswer(answer, liveProxySDP("local", "local-password", nil), dialer)
	if err != nil {
		t.Fatal(err)
	}
	defer closeCandidateTunnels(tunnels)
	if len(tunnels) != 1 || tunnels[0].target.String() != "20.42.0.20:443" {
		t.Fatalf("proxy tunnel targets = %v", tunnels)
	}
	var description sdp.SessionDescription
	if err := description.UnmarshalString(rewritten); err != nil {
		t.Fatal(err)
	}
	var candidates []string
	for _, media := range description.MediaDescriptions {
		for _, attribute := range media.Attributes {
			if attribute.IsICECandidate() {
				candidates = append(candidates, attribute.Value)
			}
		}
	}
	if len(candidates) != 1 || !strings.Contains(candidates[0], " tcp ") ||
		!strings.Contains(candidates[0], " 127.0.0.1 ") || strings.Contains(candidates[0], "20.42.0.20") {
		t.Fatalf("rewritten candidates = %v", candidates)
	}
}

func TestCodexLiveProxyRejectsUnsafeCandidateAndUnauthenticatedTunnel(t *testing.T) {
	t.Parallel()
	dialer := &liveProxyDialer{dialed: make(chan string, 1)}
	for _, target := range []string{"10.0.0.1", "127.0.0.1", "203.0.113.4"} {
		answer := liveProxySDP("remote", "password", []string{
			"2 1 tcp 1671430143 " + target + " 443 typ host tcptype passive",
		})
		if _, tunnels, err := prepareProxiedUpstreamAnswer(answer, liveProxySDP("local", "password", nil), dialer); err == nil {
			_ = closeCandidateTunnels(tunnels)
			t.Fatalf("unsafe target %s accepted", target)
		}
	}
	tunnel, err := newTCPCandidateTunnel(netip.MustParseAddrPort("20.42.0.20:443"), dialer, "remote:local", "remote-password")
	if err != nil {
		t.Fatal(err)
	}
	defer tunnel.Close()
	connection, err := net.Dial("tcp", tunnel.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	frame := liveProxyBindingFrame(t, "attacker:local", "remote-password")
	if _, err := connection.Write(frame); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	select {
	case address := <-dialer.dialed:
		t.Fatalf("unauthenticated tunnel dialed %s", address)
	case <-time.After(150 * time.Millisecond):
	}
	authorized, err := net.Dial("tcp", tunnel.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authorized.Write(liveProxyBindingFrame(t, "remote:local", "remote-password")); err != nil {
		t.Fatal(err)
	}
	defer authorized.Close()
	select {
	case address := <-dialer.dialed:
		if address != "20.42.0.20:443" {
			t.Fatalf("authenticated proxy target = %s", address)
		}
	case <-time.After(time.Second):
		t.Fatal("authenticated ICE connection never used the proxy")
	}
}

func liveProxyBindingFrame(t *testing.T, username, password string) []byte {
	t.Helper()
	message, err := stun.Build(stun.BindingRequest, stun.TransactionID,
		stun.NewUsername(username), stun.NewShortTermIntegrity(password), stun.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, len(message.Raw)+2)
	binary.BigEndian.PutUint16(frame[:2], uint16(len(message.Raw)))
	copy(frame[2:], message.Raw)
	return frame
}

func liveProxySDP(ufrag, password string, candidates []string) string {
	var result strings.Builder
	fmt.Fprintf(&result, "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\na=group:BUNDLE 0\r\n")
	fmt.Fprintf(&result, "m=audio 9 UDP/TLS/RTP/SAVPF 111\r\nc=IN IP4 0.0.0.0\r\na=mid:0\r\na=ice-ufrag:%s\r\na=ice-pwd:%s\r\n", ufrag, password)
	for _, candidate := range candidates {
		fmt.Fprintf(&result, "a=candidate:%s\r\n", candidate)
	}
	return result.String()
}
