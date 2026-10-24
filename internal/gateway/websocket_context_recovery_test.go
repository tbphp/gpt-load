package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
)

// 复用 HTTP 恢复用例的历史，加上会话标识供拒绝记录区分会话。
var websocketContextRecoveryTurn = strings.Replace(contextRecoveryRequest, `{"model":"model-a"`,
	`{"type":"response.create","model":"public","prompt_cache_key":"ws-context-recovery"`, 1)

func websocketRejection(code, message string) string {
	return fmt.Sprintf(`{"type":"error","status":400,"error":{"type":"invalid_request_error","code":%q,"message":%q}}`, code, message)
}

func rejectEncryptedContent(_, body string) string {
	if strings.Contains(body, "opaque") {
		return websocketRejection("invalid_encrypted_content", "The encrypted content could not be verified.")
	}
	return ""
}

// websocketContextRecoveryUpstream 按实际收到的账号和请求体决定拒绝或完成，并记录每一轮；
// closeAfterRejection 模拟出错后即关闭连接的上游（如 Codex 订阅会话）。
func websocketContextRecoveryUpstream(t *testing.T, observed *encryptedRecoveryBodies, closeAfterRejection bool, rejection func(key, body string) string) *httptest.Server {
	t.Helper()
	var responses atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, body, err := conn.ReadMessage()
			if err != nil {
				return
			}
			key := r.Header.Get("Authorization")
			observed.add(key, string(body))
			rejected := rejection(key, string(body))
			reply := websocketCompleted(fmt.Sprintf("resp_%d", responses.Add(1)), "")
			if rejected != "" {
				reply = []byte(rejected)
			}
			if conn.WriteMessage(websocket.TextMessage, reply) != nil || (rejected != "" && closeAfterRejection) {
				return
			}
		}
	}))
	t.Cleanup(upstream.Close)
	return upstream
}

func websocketContextRecoveryExchange(t *testing.T, conn *websocket.Conn, body string) string {
	t.Helper()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(body)); err != nil {
		t.Fatal(err)
	}
	_, reply, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	return string(reply)
}

func TestWebsocketContextRecoveryReplaysFirstTurnOnSameCredential(t *testing.T) {
	for _, test := range []struct {
		name                string
		channel             channel.ID
		closeAfterRejection bool
		rejection           func(key, body string) string
	}{
		{name: "serial", channel: channel.CLIProxyAPI, rejection: rejectEncryptedContent},
		{name: "multiplex", channel: channel.OpenAI, rejection: rejectEncryptedContent},
		// 串行连接与换号一样重新建连，上游出错后关闭连接也能重放。
		{name: "serial_upstream_closes", channel: channel.CLIProxyAPI, closeAfterRejection: true, rejection: rejectEncryptedContent},
		// 中转可能只保留错误消息，按事件正文识别。
		{name: "message_only", channel: channel.CLIProxyAPI, rejection: func(_, body string) string {
			if strings.Contains(body, "opaque") {
				return websocketRejection("invalid_request_error", "The encrypted content for item rs_old could not be verified.")
			}
			return ""
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			observed := &encryptedRecoveryBodies{}
			upstream := websocketContextRecoveryUpstream(t, observed, test.closeAfterRejection, test.rejection)
			_, engine, _ := websocketTestHandler(t, upstream.URL, test.channel)
			server := httptest.NewServer(engine)
			defer server.Close()
			if reply := websocketContextRecoveryExchange(t, dialGatewayWebsocket(t, server.URL), websocketContextRecoveryTurn); !strings.Contains(reply, `"response.completed"`) {
				t.Fatalf("first turn was not recovered: %s", reply)
			}
			keys, bodies := observed.snapshot()
			if len(bodies) != 2 || keys[1] != keys[0] || !strings.Contains(bodies[0], "opaque") {
				t.Fatalf("want one cleaned replay on the same credential: keys=%v bodies=%v", keys, bodies)
			}
			assertRecoveredContext(t, bodies[1])
		})
	}
}

func TestWebsocketContextRecoveryReplaysAfterCredentialRefresh(t *testing.T) {
	for _, id := range []channel.ID{channel.CLIProxyAPI, channel.OpenAI} {
		t.Run(string(id), func(t *testing.T) {
			observed := &encryptedRecoveryBodies{}
			var refresh atomic.Pointer[func()]
			upstream := websocketContextRecoveryUpstream(t, observed, false, func(key, body string) string {
				rejected := rejectEncryptedContent(key, body)
				// 被拒与重放之间凭据刷新：版本与密文变化，身份不变。
				if fn := refresh.Load(); rejected != "" && fn != nil {
					(*fn)()
				}
				return rejected
			})
			h, engine, _ := websocketTestHandler(t, upstream.URL, id)
			refreshed := testCredentialEntry(t, h.encryption, 1, 1, "refreshed-key")
			refreshed.Version = 2
			registry := h.registry.(*state.CredentialRegistry)
			fn := func() {
				if err := registry.ReplaceCredentials([]state.CredentialEntry{refreshed}); err != nil {
					t.Error(err)
				}
			}
			refresh.Store(&fn)
			server := httptest.NewServer(engine)
			defer server.Close()
			if reply := websocketContextRecoveryExchange(t, dialGatewayWebsocket(t, server.URL), websocketContextRecoveryTurn); !strings.Contains(reply, `"response.completed"`) {
				t.Fatalf("replay was skipped after the credential refresh: %s", reply)
			}
			if _, bodies := observed.snapshot(); len(bodies) != 2 {
				t.Fatalf("upstream turns = %d, want rejection and replay: %v", len(bodies), bodies)
			} else {
				assertRecoveredContext(t, bodies[1])
			}
		})
	}
}

func TestWebsocketContextRecoveryKeepsCleanedHistoryAcrossFailover(t *testing.T) {
	observed := &encryptedRecoveryBodies{}
	upstream := websocketContextRecoveryUpstream(t, observed, false, func(key, body string) string {
		if strings.Contains(body, "opaque") {
			return websocketRejection("invalid_encrypted_content", "The encrypted content could not be verified.")
		}
		// 首个账号清理后的重放再遇到额度错误，转由下一个账号继续。
		if keys, _ := observed.snapshot(); key == keys[0] {
			return `{"type":"error","status":429,"error":{"type":"usage_limit_reached","message":"quota exhausted"}}`
		}
		return ""
	})
	h, engine, input := websocketTestHandler(t, upstream.URL, channel.CLIProxyAPI)
	input.Credentials = append(input.Credentials, testCredentialConfig(2, 1))
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	if err := h.registry.(*state.CredentialRegistry).ReplaceCredentials([]state.CredentialEntry{
		testCredentialEntry(t, h.encryption, 1, 1, "first-key"), testCredentialEntry(t, h.encryption, 2, 1, "second-key"),
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine)
	defer server.Close()
	if reply := websocketContextRecoveryExchange(t, dialGatewayWebsocket(t, server.URL), websocketContextRecoveryTurn); !strings.Contains(reply, `"response.completed"`) {
		t.Fatalf("failover after the replay failed: %s", reply)
	}
	keys, bodies := observed.snapshot()
	if len(bodies) != 3 || keys[1] != keys[0] || keys[2] == keys[0] {
		t.Fatalf("want rejection, same-credential replay, then failover: keys=%v", keys)
	}
	assertRecoveredContext(t, bodies[1])
	assertRecoveredContext(t, bodies[2])
}

func TestWebsocketContextRecoveryStripsRememberedStateAfterReconnect(t *testing.T) {
	for _, id := range []channel.ID{channel.CLIProxyAPI, channel.OpenAI} {
		t.Run(string(id), func(t *testing.T) {
			observed := &encryptedRecoveryBodies{}
			upstream := websocketContextRecoveryUpstream(t, observed, false, rejectEncryptedContent)
			_, engine, _ := websocketTestHandler(t, upstream.URL, id)
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			if reply := websocketContextRecoveryExchange(t, conn, `{"type":"response.create","model":"public","store":false,"input":"warmup"}`); !strings.Contains(reply, `"response.completed"`) {
				t.Fatalf("warmup failed: %s", reply)
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte(websocketContextRecoveryTurn)); err != nil {
				t.Fatal(err)
			}
			if _, reply, err := conn.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseTryAgainLater) {
				t.Fatalf("bound rejection reply=%s err=%v, want close 1013", reply, err)
			}
			if reply := websocketContextRecoveryExchange(t, dialGatewayWebsocket(t, server.URL), websocketContextRecoveryTurn); !strings.Contains(reply, `"response.completed"`) {
				t.Fatalf("resent turn was rejected again: %s", reply)
			}
			_, bodies := observed.snapshot()
			if len(bodies) != 3 || !strings.Contains(bodies[1], "opaque") || strings.Contains(bodies[2], "opaque") {
				t.Fatalf("resent turn did not strip the remembered rejection: %v", bodies)
			}
			// 只移除被拒密文，条目 ID 与可读内容保持不变。
			for _, preserved := range []string{"msg_old", "rs_old", "summary", "call_keep"} {
				if !strings.Contains(bodies[2], preserved) {
					t.Fatalf("pre-strip removed %q: %s", preserved, bodies[2])
				}
			}
		})
	}
}

func TestWebsocketContextRecoveryHonorsRetryPolicy(t *testing.T) {
	rule := func(retry health.RetryDirective, effect health.Effect, cooldownSeconds int64) config.Settings {
		return config.Settings{state.SettingErrorRules: []health.ErrorRule{{
			StatusCodes: []int{http.StatusBadRequest}, Retry: retry, Effect: effect, CooldownSeconds: cooldownSeconds,
		}}}
	}
	for _, test := range []struct {
		name   string
		system config.Settings
		group  config.Settings
	}{
		{name: "retry_count_zero", system: config.Settings{state.SettingRetryCount: 0}},
		{name: "error_rule_without_retry", group: rule(health.RetryNone, health.EffectNone, 0)},
		{name: "error_rule_cooldown", group: rule(health.RetryNextCandidate, health.EffectCooldownCredential, 60)},
		{name: "error_rule_skip_group", group: rule(health.RetryNextCandidate, health.EffectSkipGroup, 0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			observed := &encryptedRecoveryBodies{}
			upstream := websocketContextRecoveryUpstream(t, observed, false, rejectEncryptedContent)
			h, engine, input := websocketTestHandler(t, upstream.URL, channel.CLIProxyAPI)
			input.SystemSettings = test.system
			input.Groups[0].Settings = test.group
			if _, err := h.manager.Publish(input); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(engine)
			defer server.Close()
			if reply := websocketContextRecoveryExchange(t, dialGatewayWebsocket(t, server.URL), websocketContextRecoveryTurn); !strings.Contains(reply, "invalid_encrypted_content") {
				t.Fatalf("upstream rejection was not returned: %s", reply)
			}
			if _, bodies := observed.snapshot(); len(bodies) != 1 {
				t.Fatalf("recovery replay ignored the retry policy: %d upstream turns", len(bodies))
			}
			if test.name != "retry_count_zero" {
				return
			}
			// 本轮不重放，但拒绝已记录，新连接的首轮发送前即清理。
			if reply := websocketContextRecoveryExchange(t, dialGatewayWebsocket(t, server.URL), websocketContextRecoveryTurn); !strings.Contains(reply, `"response.completed"`) {
				t.Fatalf("next turn did not reuse the remembered rejection: %s", reply)
			}
			if _, bodies := observed.snapshot(); len(bodies) != 2 || strings.Contains(bodies[1], "opaque") {
				t.Fatalf("next turn was sent unchanged: %v", bodies)
			}
		})
	}
}

func TestWebsocketContextRecoveryRemembersOnlyRejectedItemIDs(t *testing.T) {
	observed := &encryptedRecoveryBodies{}
	upstream := websocketContextRecoveryUpstream(t, observed, false, func(_, body string) string {
		if strings.Contains(body, "msg_old") {
			return websocketRejection("item_not_found", "Item with id 'msg_old' not found.")
		}
		return ""
	})
	_, engine, _ := websocketTestHandler(t, upstream.URL, channel.CLIProxyAPI)
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	for turn := 0; turn < 2; turn++ {
		if reply := websocketContextRecoveryExchange(t, conn, websocketContextRecoveryTurn); !strings.Contains(reply, `"response.completed"`) {
			t.Fatalf("turn %d failed: %s", turn+1, reply)
		}
	}
	_, bodies := observed.snapshot()
	if len(bodies) != 3 {
		t.Fatalf("upstream turns = %d, want 3: %v", len(bodies), bodies)
	}
	assertRecoveredContext(t, bodies[1])
	if strings.Contains(bodies[2], "msg_old") || !strings.Contains(bodies[2], "opaque") {
		t.Fatalf("item-ID rejection must strip only item IDs on later turns: %s", bodies[2])
	}
}
