package state

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/outboundproxy"
)

func TestCompileResolvesImmutableGroupProxy(t *testing.T) {
	t.Parallel()

	global := outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: "http://global.example.com:8080"}
	groupOverride := outboundproxy.Config{Mode: outboundproxy.ModeDirect}
	snapshot, err := Compile(CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		GlobalProxy:     &global,
		Groups: []GroupConfig{
			{ID: 1, Name: "inherit", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []ModelConfig{{ID: "gpt-4o"}}, Enabled: true},
			{ID: 2, Name: "direct", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []ModelConfig{{ID: "gpt-4o"}}, Proxy: &groupOverride, Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if snapshot.GlobalProxy.Source != outboundproxy.SourceGlobal || snapshot.GlobalProxy.Config.URL != global.URL {
		t.Fatalf("GlobalProxy = %#v", snapshot.GlobalProxy)
	}
	if got := snapshot.Groups[1].Proxy; got.Source != outboundproxy.SourceGlobal || got.Config.URL != global.URL {
		t.Fatalf("inherited Group proxy = %#v", got)
	}
	if got := snapshot.Groups[2].Proxy; got.Source != outboundproxy.SourceGroup || got.Config.Mode != outboundproxy.ModeDirect {
		t.Fatalf("direct Group proxy = %#v", got)
	}

	global.URL = "http://mutated.example.com"
	groupOverride.Mode = outboundproxy.ModeCustom
	if snapshot.Groups[1].Proxy.Config.URL != "http://global.example.com:8080" || snapshot.Groups[2].Proxy.Config.Mode != outboundproxy.ModeDirect {
		t.Fatalf("snapshot retained caller aliases: %#v", snapshot.Groups)
	}
}
