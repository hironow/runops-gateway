package rpc

import "testing"

// TestParseStrictBearer_RejectsControlChar covers the defensive control-char
// reject path. Go's net/http client pre-validates Authorization values and
// would not allow this byte to reach a httptest server, but raw-socket
// clients and reverse proxies can pass it through.
func TestParseStrictBearer_RejectsControlChar(t *testing.T) {
	cases := map[string]string{
		"null-byte":       "Bearer tok\x00garbage",
		"newline":         "Bearer tok\nafter",
		"carriage-return": "Bearer tok\rafter",
		"vertical-tab":    "Bearer tok\vafter",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, ok := parseStrictBearer(raw); ok {
				t.Errorf("parseStrictBearer(%q) accepted; want rejected", raw)
			}
		})
	}
}

func TestParseStrictBearer_AcceptsValid(t *testing.T) {
	for _, raw := range []string{
		"Bearer token-1",
		"bearer ABC-123",
		"BEARER deadbeef",
	} {
		if _, ok := parseStrictBearer(raw); !ok {
			t.Errorf("parseStrictBearer(%q) rejected; want accepted", raw)
		}
	}
}
