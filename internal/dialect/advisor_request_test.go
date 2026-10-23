package dialect

import (
	"net/http"
	"slices"
	"testing"

	"gpt-load/internal/execution"
)

func TestInspectRequestAdvisorModels(t *testing.T) {
	const messages = `"max_tokens":16,"messages":[{"role":"user","content":"advisor"}]`
	for _, test := range []struct {
		name    string
		dialect Dialect
		path    string
		body    string
		want    []string
		wantErr bool
	}{
		{name: "anthropic advisor tool", dialect: NewAnthropic(), path: "/v1/messages",
			body: `{"model":"claude-haiku-4-5",` + messages + `,"tools":[{"type":"web_search_20250305","name":"web_search"},` +
				`{"type":"advisor_20260301","name":"advisor","model":"claude-opus-4-8"}]}`,
			want: []string{"claude-opus-4-8"}},
		{name: "escaped advisor type", dialect: NewAnthropic(), path: "/v1/messages",
			body: `{"model":"claude-haiku-4-5",` + messages + `,"tools":[{"type":"advisor_20260301","name":"advisor","model":"claude-opus-4-8"}]}`,
			want: []string{"claude-opus-4-8"}},
		{name: "keys match without case", dialect: NewAnthropic(), path: "/v1/messages",
			body: `{"model":"claude-haiku-4-5",` + messages + `,"Tools":[{"TYPE":"advisor_20260301","Model":"claude-opus-4-8"}]}`,
			want: []string{"claude-opus-4-8"}},
		{name: "case folded duplicate type", dialect: NewAnthropic(), path: "/v1/messages",
			body: `{"model":"claude-haiku-4-5",` + messages + `,"tools":[{"type":"custom","Type":"advisor_20260301","model":"claude-opus-4-8"}]}`, wantErr: true},
		{name: "repeated advisor model is listed once", dialect: NewAnthropic(), path: "/v1/messages",
			body: `{"model":"claude-haiku-4-5",` + messages + `,"tools":[{"type":"advisor_20260301","model":"claude-opus-4-8"},` +
				`{"type":"advisor_20260401","model":"claude-opus-4-8"},{"type":"advisor_20260401","model":"claude-sonnet-5-5"}]}`,
			want: []string{"claude-opus-4-8", "claude-sonnet-5-5"}},
		{name: "other tools keep their model field", dialect: NewAnthropic(), path: "/v1/messages",
			body: `{"model":"claude-haiku-4-5",` + messages + `,"tools":[{"type":"custom","name":"advisor","model":"x"}]}`},
		{name: "no tools", dialect: NewAnthropic(), path: "/v1/messages",
			body: `{"model":"claude-haiku-4-5",` + messages + `}`},
		{name: "count tokens does not run the advisor", dialect: NewAnthropic(), path: "/v1/messages/count_tokens",
			body: `{"model":"claude-haiku-4-5",` + messages + `,"tools":[{"type":"advisor_20260301","model":"claude-opus-4-8"}]}`},
		{name: "advisor tool without model", dialect: NewAnthropic(), path: "/v1/messages",
			body: `{"model":"claude-haiku-4-5",` + messages + `,"tools":[{"type":"advisor_20260301","name":"advisor"}]}`, wantErr: true},
		{name: "ambiguous advisor model", dialect: NewAnthropic(), path: "/v1/messages",
			body: `{"model":"claude-haiku-4-5",` + messages + `,"tools":[{"type":"advisor_20260301","model":"claude-haiku-4-5","model":"claude-opus-4-8"}]}`, wantErr: true},
		{name: "ambiguous tool type", dialect: NewAnthropic(), path: "/v1/messages",
			body: `{"model":"claude-haiku-4-5",` + messages + `,"tools":[{"type":"custom","model":"claude-opus-4-8","type":"advisor_20260301"}]}`, wantErr: true},
		{name: "ambiguous tools", dialect: NewAnthropic(), path: "/v1/messages",
			body: `{"model":"claude-haiku-4-5",` + messages + `,"tools":[],"tools":[{"type":"advisor_20260301","model":"claude-opus-4-8"}]}`, wantErr: true},
		{name: "responses advisor tool", dialect: NewOpenAIResponses(), path: "/v1/responses",
			body: `{"model":"claude-haiku-4-5","input":"advisor","tools":[{"type":"advisor","model":"claude-opus-4-8"}]}`,
			want: []string{"claude-opus-4-8"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			metadata, err := test.dialect.InspectRequest(&ParsedRequest{
				Method: http.MethodPost, Path: test.path, Header: http.Header{}, Body: []byte(test.body),
			})
			if test.wantErr {
				if err == nil {
					t.Fatalf("InspectRequest() advisor models = %v, want error", metadata.AdvisorModels)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(metadata.AdvisorModels, test.want) {
				t.Fatalf("advisor models = %v, want %v", metadata.AdvisorModels, test.want)
			}
			if len(test.want) > 0 && metadata.RouteRequirement != execution.RouteRequirementNative {
				t.Fatalf("route requirement = %q, want native for advisor tools", metadata.RouteRequirement)
			}
		})
	}
}
