package warp_ws

import (
	"net/http/httptest"
	"testing"
)

func TestDefaultCheckOrigin(t *testing.T) {
	t.Run("no origin header is allowed", func(t *testing.T) {
		r := httptest.NewRequest("GET", "http://example.com/ws", nil)
		r.Host = "example.com"
		if !defaultCheckOrigin(r) {
			t.Fatal("expected request without Origin to be allowed")
		}
	})

	t.Run("matching host is allowed", func(t *testing.T) {
		r := httptest.NewRequest("GET", "http://example.com/ws", nil)
		r.Host = "example.com:8080"
		r.Header.Set("Origin", "http://EXAMPLE.com:8080")
		if !defaultCheckOrigin(r) {
			t.Fatal("expected matching origin host to be allowed")
		}
	})

	t.Run("different host is rejected", func(t *testing.T) {
		r := httptest.NewRequest("GET", "http://example.com/ws", nil)
		r.Host = "example.com"
		r.Header.Set("Origin", "http://evil.example")
		if defaultCheckOrigin(r) {
			t.Fatal("expected mismatched origin host to be rejected")
		}
	})

	t.Run("invalid origin is rejected", func(t *testing.T) {
		r := httptest.NewRequest("GET", "http://example.com/ws", nil)
		r.Host = "example.com"
		r.Header.Set("Origin", "://bad-origin")
		if defaultCheckOrigin(r) {
			t.Fatal("expected invalid origin to be rejected")
		}
	})
}

func TestParseListenEndpoint(t *testing.T) {
	t.Run("raw address uses default path", func(t *testing.T) {
		addr, path, err := parseListenEndpoint(":8080", "/ws")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if addr != ":8080" || path != "/ws" {
			t.Fatalf("unexpected result addr=%q path=%q", addr, path)
		}
	})

	t.Run("url without path uses default path", func(t *testing.T) {
		addr, path, err := parseListenEndpoint("ws://localhost:9000", "/rpc")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if addr != "localhost:9000" || path != "/rpc" {
			t.Fatalf("unexpected result addr=%q path=%q", addr, path)
		}
	})

	t.Run("url with explicit path keeps path", func(t *testing.T) {
		addr, path, err := parseListenEndpoint("ws://localhost:9000/custom", "/rpc")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if addr != "localhost:9000" || path != "/custom" {
			t.Fatalf("unexpected result addr=%q path=%q", addr, path)
		}
	})
}
