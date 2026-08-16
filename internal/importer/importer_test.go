package importer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNameFor(t *testing.T) {
	cases := []struct {
		entry NodeEntry
		want  string
	}{
		{NodeEntry{Pubkey: "abcdefghijklmnop"}, "imported-abcdefghijkl"},
		{NodeEntry{Pubkey: "short"}, "imported-short"},
		{NodeEntry{IP: "1.2.3.4", GRPCPort: 18142}, "imported-1.2.3.4-18142"},
	}
	for _, c := range cases {
		if got := nameFor(c.entry); got != c.want {
			t.Errorf("nameFor(%+v) = %q, want %q", c.entry, got, c.want)
		}
	}
}

func TestImporter_FetchDecodesValidResponse(t *testing.T) {
	entries := []NodeEntry{{Pubkey: "pk1", IP: "1.2.3.4", P2PPort: 18189, GRPCPort: 18102}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(entries)
	}))
	defer srv.Close()

	imp := New(nil, srv.URL, 0)
	got, err := imp.fetch(t.Context())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(got) != 1 || got[0].Pubkey != "pk1" {
		t.Errorf("fetch returned %+v, want one entry with pubkey pk1", got)
	}
}

func TestImporter_FetchHandlesUnreachableGracefully(t *testing.T) {
	// A closed-immediately server simulates "endpoint doesn't exist yet" — fetch
	// must return an error, not panic, so runOnce can log-and-skip.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	imp := New(nil, srv.URL, 0)
	if _, err := imp.fetch(t.Context()); err == nil {
		t.Fatal("expected an error fetching from a closed server, got nil")
	}
}

func TestImporter_FetchHandlesMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = strings.NewReader("not json").WriteTo(w)
	}))
	defer srv.Close()

	imp := New(nil, srv.URL, 0)
	if _, err := imp.fetch(t.Context()); err == nil {
		t.Fatal("expected a decode error for malformed JSON, got nil")
	}
}

func TestImporter_FetchHandlesNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	imp := New(nil, srv.URL, 0)
	if _, err := imp.fetch(t.Context()); err == nil {
		t.Fatal("expected an error for a non-200 response, got nil")
	}
}
