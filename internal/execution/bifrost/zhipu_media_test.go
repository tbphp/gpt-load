package bifrost

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"

	"github.com/maximhq/bifrost/core/schemas"
)

func zhipuTestTarget() channel.ResolvedTarget {
	targetConfig, _ := json.Marshal(map[string]string{"base_url": "https://open.bigmodel.cn/api/paas/v4"})
	return channel.ResolvedTarget{
		ChannelID: channel.ZhipuAI, ProviderKind: channel.ProviderOpenAICompatible, TargetConfig: targetConfig,
	}
}

func TestPrepareZhipuMediaPaths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		operation execution.Operation
		method    string
		path      string
		wantPath  string
		wantBody  bool
	}{
		{"sync image", execution.OperationZhipuImagesGenerate, http.MethodPost, "/v1/images/generations", "/images/generations", true},
		{"async image", execution.OperationZhipuImagesCreateAsync, http.MethodPost, "/v1/async/images/generations", "/async/images/generations", true},
		{"video", execution.OperationZhipuVideosCreate, http.MethodPost, "/v1/videos/generations", "/videos/generations", true},
		{"retrieve", execution.OperationZhipuMediaRetrieve, http.MethodGet, "/v1/async-result/zhipu_1", "/async-result/zhipu_1", false},
		{"cancel", execution.OperationZhipuMediaCancel, http.MethodDelete, "/v1/async-result/zhipu_1", "/async-result/zhipu_1", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			spec := execution.AttemptSpec{
				ClientProtocol: protocol.ZhipuMedia, Operation: test.operation, RouteMode: execution.RouteNative,
				Method: test.method, Path: test.path, UpstreamModel: "CogView-3-Flash",
				Header: http.Header{"Content-Type": {"application/json"}},
				Body:   []byte(`{"model":"CogView-3-Flash","prompt":"fox"}`),
			}
			if !test.wantBody {
				spec.Body = nil
			}
			prepared, failure := prepareZhipuMedia(spec, zhipuTestTarget(), schemas.ModelProvider("openai"), schemas.Key{}, nil)
			if failure != nil {
				t.Fatalf("failure = %#v", failure)
			}
			if prepared.passthrough == nil || prepared.passthrough.Path != test.wantPath ||
				prepared.passthrough.Method != test.method ||
				prepared.passthrough.UpstreamURL != "https://open.bigmodel.cn/api/paas/v4" {
				t.Fatalf("prepared = %#v", prepared.passthrough)
			}
			if test.wantBody && !strings.Contains(string(prepared.passthrough.Body), `"model":"CogView-3-Flash"`) {
				t.Fatalf("body = %s", prepared.passthrough.Body)
			}
			if !test.wantBody && len(prepared.passthrough.Body) != 0 {
				t.Fatalf("unexpected body = %s", prepared.passthrough.Body)
			}
		})
	}
}

func TestValidZhipuMediaShape(t *testing.T) {
	t.Parallel()
	base := execution.AttemptSpec{
		ClientProtocol: protocol.ZhipuMedia, RouteMode: execution.RouteNative, Method: http.MethodPost,
		Path: "/v1/videos/generations", Body: []byte(`{"model":"v","prompt":"p"}`), Operation: execution.OperationZhipuVideosCreate,
	}
	if !validZhipuMediaShape(base, false) || validZhipuMediaShape(base, true) {
		t.Fatal("video create shape must be unary only")
	}
	retrieve := base.Clone()
	retrieve.Method, retrieve.Path, retrieve.Body, retrieve.Operation = http.MethodGet, "/v1/async-result/zhipu_1", nil, execution.OperationZhipuMediaRetrieve
	if !validZhipuMediaShape(retrieve, false) {
		t.Fatal("retrieve shape rejected")
	}
	cancel := retrieve.Clone()
	cancel.Method, cancel.Operation = http.MethodDelete, execution.OperationZhipuMediaCancel
	if !validZhipuMediaShape(cancel, false) {
		t.Fatal("cancel shape rejected")
	}
	for _, invalid := range []execution.AttemptSpec{
		{ClientProtocol: protocol.ZhipuMedia, RouteMode: execution.RouteNative, Method: http.MethodGet, Path: "/v1/async-result/a/b", Operation: execution.OperationZhipuMediaRetrieve},
		{ClientProtocol: protocol.ZhipuMedia, RouteMode: execution.RouteNative, Method: http.MethodPost, Path: "/v1/images/generations", Operation: execution.OperationZhipuImagesGenerate},
	} {
		if validZhipuMediaShape(invalid, false) {
			t.Fatalf("invalid shape accepted: %+v", invalid)
		}
	}
}
