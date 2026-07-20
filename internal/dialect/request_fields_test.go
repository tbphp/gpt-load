package dialect

import (
	"bytes"
	"testing"
)

func TestExtractJSONRequestFieldsStrictDecisionContract(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantModel  string
		wantStream bool
		wantErr    bool
	}{
		{name: "minimal", body: `{"model":"gpt-4o"}`, wantModel: "gpt-4o"},
		{name: "stream true", body: `{"model":"gpt-4o","stream":true}`, wantModel: "gpt-4o", wantStream: true},
		{name: "unknown nested fields", body: `{"metadata":{"model":"ignored","stream":true},"model":"gpt-4o"}`, wantModel: "gpt-4o"},
		{name: "escaped field name", body: `{"\u006dodel":"gpt-4o"}`, wantModel: "gpt-4o"},
		{name: "missing model", body: `{}`, wantErr: true},
		{name: "model null", body: `{"model":null}`, wantErr: true},
		{name: "model number", body: `{"model":1}`, wantErr: true},
		{name: "model boundary whitespace", body: `{"model":" gpt-4o"}`, wantErr: true},
		{name: "duplicate model", body: `{"model":"a","model":"b"}`, wantErr: true},
		{name: "case alias", body: `{"Model":"gpt-4o"}`, wantErr: true},
		{name: "case collision first exact", body: `{"model":"forbidden","Model":"allowed"}`, wantErr: true},
		{name: "case collision first alias", body: `{"Model":"allowed","model":"forbidden"}`, wantErr: true},
		{name: "stream null", body: `{"model":"gpt-4o","stream":null}`, wantErr: true},
		{name: "stream string", body: `{"model":"gpt-4o","stream":"true"}`, wantErr: true},
		{name: "duplicate stream", body: `{"model":"gpt-4o","stream":false,"stream":true}`, wantErr: true},
		{name: "stream case alias", body: `{"model":"gpt-4o","Stream":true}`, wantErr: true},
		{name: "stream unicode simple-fold alias", body: `{"model":"gpt-4o","ſtream":true}`, wantErr: true},
		{name: "stream unicode simple-fold collision", body: `{"model":"gpt-4o","stream":false,"ſtream":true}`, wantErr: true},
		{name: "array root", body: `[{"model":"gpt-4o"}]`, wantErr: true},
		{name: "trailing value", body: `{"model":"gpt-4o"}{}`, wantErr: true},
		{name: "invalid utf8", body: string([]byte{'{', '"', 'm', 'o', 'd', 'e', 'l', '"', ':', '"', 0xff, '"', '}'}), wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(test.body)
			before := bytes.Clone(body)
			model, stream, err := extractJSONRequestFields(body)
			if test.wantErr {
				if err == nil {
					t.Fatalf("extractJSONRequestFields() = %q, %t, nil", model, stream)
				}
			} else if err != nil || model != test.wantModel || stream != test.wantStream {
				t.Fatalf("extractJSONRequestFields() = %q, %t, %v", model, stream, err)
			}
			if !bytes.Equal(body, before) {
				t.Fatal("parser mutated wire body")
			}
		})
	}
}
