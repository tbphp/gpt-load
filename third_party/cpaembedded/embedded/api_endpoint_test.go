package embedded

import "testing"

func TestResolveAPIEndpointPreservesNativePathsAndQueries(t *testing.T) {
	for _, test := range []struct {
		name, root, official, want string
	}{
		{"official", "", "https://cli-chat-proxy.grok.com/v1/billing?format=credits", "https://cli-chat-proxy.grok.com/v1/billing?format=credits"},
		{"origin", "https://relay.example/team-a", "https://api.anthropic.com", "https://relay.example/team-a"},
		{"root", "https://relay.example", "https://chatgpt.com/backend-api/wham/usage", "https://relay.example/backend-api/wham/usage"},
		{"prefix", " HTTPS://RELAY.EXAMPLE:443/team-a/ ", "https://chatgpt.com/backend-api/codex/models", "https://relay.example/team-a/backend-api/codex/models"},
		{"query", "https://relay.example/team-a", "https://cli-chat-proxy.grok.com/v1/billing?format=credits", "https://relay.example/team-a/v1/billing?format=credits"},
		{"literal prefix", "https://relay.example/v1", "https://api.anthropic.com/v1/messages", "https://relay.example/v1/v1/messages"},
		{"escaped prefix", "https://relay.example/team%2Fa", "https://api.anthropic.com/v1/messages", "https://relay.example/team%2Fa/v1/messages"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveAPIEndpoint(test.root, test.official)
			if err != nil || got != test.want {
				t.Fatalf("ResolveAPIEndpoint() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestResolveAPIEndpointRejectsUnsafeRoots(t *testing.T) {
	for _, root := range []string{"http://relay.example", "relative", "https://user:secret@relay.example", "https://relay.example?x=1", "https://relay.example?", "https://relay.example#fragment", "https://relay.example#"} {
		t.Run(root, func(t *testing.T) {
			if _, err := ResolveAPIEndpoint(root, "https://api.anthropic.com/v1/messages"); err == nil {
				t.Fatal("accepted an invalid API proxy root")
			}
		})
	}
}
