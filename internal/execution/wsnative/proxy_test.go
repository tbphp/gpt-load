package wsnative

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/outboundproxy"
)

func TestWebsocketProxyModes(t *testing.T) {
	if endpoint := os.Getenv("GPT_LOAD_WS_PROXY_FIXTURE"); endpoint != "" {
		runProxyTurn(t, endpoint, outboundproxy.Effective{Config: outboundproxy.Config{Mode: outboundproxy.ModeEnvironment}, Source: outboundproxy.SourceDefault})
		return
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"resp_proxy","object":"response","status":"completed"}}`))
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	var tunnels atomic.Int32
	httpProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Error("proxy did not receive CONNECT")
			w.WriteHeader(400)
			return
		}
		client, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		target, err := net.DialTimeout("tcp", u.Host, time.Second)
		if err != nil {
			t.Error(err)
			return
		}
		defer target.Close()
		tunnels.Add(1)
		_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		if rw.Flush() != nil {
			return
		}
		relayProxy(client, target)
	}))
	defer httpProxy.Close()
	const target = "ws://websocket-fixture.invalid/responses"
	t.Run("http", func(t *testing.T) {
		runProxyTurn(t, target, outboundproxy.Effective{Config: outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: httpProxy.URL}, Source: outboundproxy.SourceGroup})
	})
	t.Run("environment", func(t *testing.T) {
		// net/http 缓存环境代理；隔离子进程避免与其他测试的首次读取互相影响。
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestWebsocketProxyModes$")
		for _, entry := range os.Environ() {
			key, _, _ := strings.Cut(entry, "=")
			switch strings.ToLower(key) {
			case "http_proxy", "https_proxy", "all_proxy", "no_proxy":
				continue
			}
			cmd.Env = append(cmd.Env, entry)
		}
		cmd.Env = append(cmd.Env, "GPT_LOAD_WS_PROXY_FIXTURE="+target, "HTTP_PROXY="+httpProxy.URL, "HTTPS_PROXY="+httpProxy.URL, "NO_PROXY=")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("environment proxy: %v %s", err, output)
		}
	})
	if tunnels.Load() != 2 {
		t.Fatalf("HTTP proxy tunnels=%d", tunnels.Load())
	}
	t.Run("socks5", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			client, err := listener.Accept()
			if err != nil {
				return
			}
			defer client.Close()
			_ = client.SetDeadline(time.Now().Add(3 * time.Second))
			greeting := make([]byte, 2)
			if _, err = io.ReadFull(client, greeting); err != nil {
				return
			}
			methods := make([]byte, int(greeting[1]))
			if _, err = io.ReadFull(client, methods); err != nil {
				return
			}
			if _, err = client.Write([]byte{5, 0}); err != nil {
				return
			}
			request := make([]byte, 5)
			if _, err = io.ReadFull(client, request); err != nil {
				return
			}
			if request[0] != 5 || request[1] != 1 || request[3] != 3 {
				t.Error("unexpected SOCKS request")
				return
			}
			address := make([]byte, int(request[4])+2)
			if _, err = io.ReadFull(client, address); err != nil {
				return
			}
			target, err := net.DialTimeout("tcp", u.Host, time.Second)
			if err != nil {
				t.Error(err)
				return
			}
			defer target.Close()
			if _, err = client.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
				return
			}
			relayProxy(client, target)
		}()
		runProxyTurn(t, target, outboundproxy.Effective{Config: outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: "socks5://" + listener.Addr().String()}, Source: outboundproxy.SourceGroup})
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("SOCKS tunnel did not close")
		}
	})
}

func relayProxy(client, target net.Conn) {
	finished := make(chan struct{})
	go func() { defer close(finished); _, _ = io.Copy(target, client); _ = target.Close() }()
	_, _ = io.Copy(client, target)
	_ = client.Close()
	<-finished
}

func runProxyTurn(t *testing.T, endpoint string, effective outboundproxy.Effective) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	s, result := Dial(ctx, endpoint, nil, effective)
	if result.Error != nil || s == nil {
		t.Fatalf("proxy dial=%+v", result)
	}
	defer s.Close()
	result = s.ExecuteTurn(ctx, []byte(`{"model":"test","input":"proxy"}`), nil)
	if result.Error != nil {
		t.Fatalf("proxy turn=%+v", result)
	}
}
