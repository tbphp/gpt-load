package outboundproxy

import "testing"

func TestManagedReferenceIsPersistableButNotExecutable(t *testing.T) {
	config, err := Decode(`{"mode":"custom","proxy_id":42}`)
	if err != nil {
		t.Fatalf("decode managed reference: %v", err)
	}
	encoded, err := Encode(config)
	if err != nil || encoded != `{"mode":"custom","proxy_id":42}` {
		t.Fatalf("reference round trip = %q, %v", encoded, err)
	}
	if _, err := Resolve(&config, nil, nil, nil); err == nil {
		t.Fatal("unresolved reference must not reach the network")
	}
}

func TestProxyCanonicalIdentityNormalizesHostAndDefaultPort(t *testing.T) {
	left, err := CanonicalConnection(Config{Mode: ModeCustom, URL: "http://EXAMPLE.COM:080/"})
	if err != nil {
		t.Fatal(err)
	}
	right, err := CanonicalConnection(Config{Mode: ModeCustom, URL: "http://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("equivalent endpoints were not normalized: %#v / %#v", left, right)
	}
}
