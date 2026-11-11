package bifrost

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func videoTestTarget(channelID channel.ID, provider channel.ProviderKind, baseURL string) channel.ResolvedTarget {
	targetConfig := json.RawMessage(`{}`)
	if baseURL != "" {
		targetConfig, _ = json.Marshal(map[string]string{"base_url": baseURL})
	}
	return channel.ResolvedTarget{ChannelID: channelID, ProviderKind: provider, TargetConfig: targetConfig}
}

func TestVideoCreateTargets(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		target   channel.ResolvedTarget
		path     string
		upstream string
	}{
		{name: "openai", target: videoTestTarget(channel.OpenAI, channel.ProviderOpenAI, ""), path: "/videos"},
		{name: "compatible", target: videoTestTarget(channel.OpenAICompatible, channel.ProviderOpenAICompatible, "https://relay.example/v1"), path: "/videos", upstream: "https://relay.example/v1"},
		{name: "gateway", target: videoTestTarget(channel.GPTLoad, channel.ProviderMultiProtocolGateway, "https://gateway.example"), path: "/v1/videos", upstream: "https://gateway.example"},
		{name: "agnes", target: videoTestTarget(channel.Agnes, channel.ProviderOpenAICompatible, "https://apihub.agnes-ai.com/v1"), path: "/videos", upstream: "https://apihub.agnes-ai.com/v1"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			spec := execution.AttemptSpec{Operation: execution.OperationVideosCreate, UpstreamModel: "video-model"}
			path, upstream, failure := videoCreateTarget(spec, test.target)
			if failure != nil {
				t.Fatalf("failure = %#v", failure)
			}
			if path != test.path || upstream != test.upstream {
				t.Fatalf("target = %q %q, want %q %q", path, upstream, test.path, test.upstream)
			}
		})
	}
}

func TestAgnesVideoResourceTargetUsesAgnesQueryEndpoint(t *testing.T) {
	t.Parallel()
	target := videoTestTarget(channel.Agnes, channel.ProviderOpenAICompatible, "https://apihub.agnes-ai.com/v1")
	spec := execution.AttemptSpec{
		Method: http.MethodGet, Path: "/v1/videos/video_up_1", Operation: execution.OperationVideosRetrieve,
		UpstreamModel: "agnes-video-2.5-flash",
	}
	path, upstream, rawQuery, failure := videoResourceTarget(spec, target)
	if failure != nil {
		t.Fatalf("failure = %#v", failure)
	}
	if path != "/agnesapi" || upstream != "https://apihub.agnes-ai.com" {
		t.Fatalf("target = %q %q", path, upstream)
	}
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		t.Fatal(err)
	}
	if query.Get("video_id") != "video_up_1" || query.Get("model_name") != "agnes-video-2.5-flash" {
		t.Fatalf("query = %q", rawQuery)
	}
}

func TestVideoResourceTargets(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		target   channel.ResolvedTarget
		path     string
		upstream string
	}{
		{name: "openai", target: videoTestTarget(channel.OpenAI, channel.ProviderOpenAI, ""), path: "/videos/video_up_1"},
		{name: "compatible", target: videoTestTarget(channel.OpenAICompatible, channel.ProviderOpenAICompatible, "https://relay.example/v1"), path: "/videos/video_up_1", upstream: "https://relay.example/v1"},
		{name: "gateway", target: videoTestTarget(channel.GPTLoad, channel.ProviderMultiProtocolGateway, "https://gateway.example"), path: "/v1/videos/video_up_1", upstream: "https://gateway.example"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			spec := execution.AttemptSpec{
				Method: http.MethodGet, Path: "/v1/videos/video_up_1", Operation: execution.OperationVideosRetrieve,
				UpstreamModel: "video-model",
			}
			path, upstream, rawQuery, failure := videoResourceTarget(spec, test.target)
			if failure != nil || rawQuery != "" {
				t.Fatalf("failure = %#v rawQuery = %q", failure, rawQuery)
			}
			if path != test.path || upstream != test.upstream {
				t.Fatalf("target = %q %q, want %q %q", path, upstream, test.path, test.upstream)
			}
		})
	}
}

func TestSupportedVideoRequestShape(t *testing.T) {
	t.Parallel()
	base := execution.AttemptSpec{
		ClientProtocol: protocol.OpenAIVideos, RouteMode: execution.RouteNative, Method: http.MethodPost,
		Path: "/v1/videos", Body: []byte(`{"model":"m","prompt":"p"}`),
		Operation: execution.OperationVideosCreate,
	}
	if !validVideoRequestShape(base, false) || validVideoRequestShape(base, true) {
		t.Fatal("create shape must be unary only and valid")
	}
	retrieve := base.Clone()
	retrieve.Method, retrieve.Path, retrieve.Body, retrieve.Operation = http.MethodGet, "/v1/videos/video_1", nil, execution.OperationVideosRetrieve
	if !validVideoRequestShape(retrieve, false) {
		t.Fatal("retrieve shape was rejected")
	}
	content := retrieve.Clone()
	content.Path, content.Operation = "/v1/videos/video_1/content", execution.OperationVideosContent
	if !validVideoRequestShape(content, false) {
		t.Fatal("content shape was rejected")
	}
	remove := retrieve.Clone()
	remove.Method, remove.Operation = http.MethodDelete, execution.OperationVideosDelete
	if !validVideoRequestShape(remove, false) {
		t.Fatal("delete shape was rejected")
	}
	invalid := []execution.AttemptSpec{
		{ClientProtocol: protocol.OpenAIVideos, RouteMode: execution.RouteNative, Method: http.MethodPost, Path: "/v1/videos", Operation: execution.OperationVideosCreate},
		{ClientProtocol: protocol.OpenAIVideos, RouteMode: execution.RouteNative, Method: http.MethodGet, Path: "/v1/videos/../x", Operation: execution.OperationVideosRetrieve},
		{ClientProtocol: protocol.OpenAIVideos, RouteMode: execution.RouteNative, Method: http.MethodGet, Path: "/v1/videos/x/other", Operation: execution.OperationVideosContent},
	}
	for _, spec := range invalid {
		if validVideoRequestShape(spec, false) {
			t.Fatalf("invalid shape accepted: %+v", spec)
		}
	}
}
