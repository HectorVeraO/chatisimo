package capitime_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
)

func newTLSImageServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()

	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)

	return srv
}

func newTestFetcher(srv *httptest.Server) *capitime.HTTPImageFetcher {
	return capitime.NewHTTPImageFetcher(capitime.HTTPImageFetcherConfig{
		Client:       srv.Client(),
		AllowedHosts: []string{srv.Listener.Addr().String()},
	})
}

func TestHTTPImageFetcherFetchesFromAllowedHost(t *testing.T) {
	t.Parallel()

	srv := newTLSImageServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("synthetic png bytes"))
	})

	got, err := newTestFetcher(srv).Fetch(t.Context(), srv.URL+"/a.png?ex=abc", 1024)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(got) != "synthetic png bytes" {
		t.Errorf("Fetch = %q, want the served body", got)
	}
}

func TestHTTPImageFetcherEnforcesSizeLimit(t *testing.T) {
	t.Parallel()

	const limit = 10

	cases := []struct {
		name    string
		body    []byte
		chunked bool
		wantErr bool
	}{
		{name: "accepts a body at the limit", body: bytes.Repeat([]byte{'a'}, limit)},
		{name: "rejects a body one byte over the limit", body: bytes.Repeat([]byte{'a'}, limit+1), wantErr: true},
		{name: "rejects an oversize body of unknown length", body: bytes.Repeat([]byte{'a'}, 4*limit), chunked: true, wantErr: true},
		{name: "rejects an empty body", body: nil, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := newTLSImageServer(t, func(w http.ResponseWriter, _ *http.Request) {
				if tc.chunked {
					w.(http.Flusher).Flush()
				}
				_, _ = w.Write(tc.body)
			})

			got, err := newTestFetcher(srv).Fetch(t.Context(), srv.URL+"/a.png", limit)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Fetch returned %d bytes, want an error", len(got))
				}
				return
			}
			if err != nil || len(got) != limit {
				t.Fatalf("Fetch = %d bytes, %v, want %d bytes", len(got), err, limit)
			}
		})
	}
}

func TestHTTPImageFetcherRejectsNon200(t *testing.T) {
	t.Parallel()

	srv := newTLSImageServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	})

	if _, err := newTestFetcher(srv).Fetch(t.Context(), srv.URL+"/a.png", 1024); err == nil {
		t.Fatal("Fetch succeeded on HTTP 404, want error")
	}
}

func TestHTTPImageFetcherRefusesDisallowedAddressesWithoutRequest(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	srv := newTLSImageServer(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("x"))
	})

	other := newTLSImageServer(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("x"))
	})

	fetcher := newTestFetcher(srv)
	addr := srv.Listener.Addr().String()

	cases := []struct {
		name string
		url  string
	}{
		{"host outside the allowlist", other.URL + "/a.png"},
		{"plain HTTP on an allowed host", "http://" + addr + "/a.png"},
		{"credentials in the URL", "https://user:pass@" + addr + "/a.png"},
		{"look-alike host suffix", "https://" + addr + ".example.com/a.png"},
		{"relative URL", "/a.png"},
		{"empty URL", ""},
		{"unsupported scheme", "file:///etc/passwd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := fetcher.Fetch(t.Context(), tc.url, 1024); err == nil {
				t.Errorf("Fetch(%q) succeeded, want error", tc.name)
			}
		})
	}

	t.Cleanup(func() {
		if got := hits.Load(); got != 0 {
			t.Errorf("servers received %d requests, want 0", got)
		}
	})
}

func TestHTTPImageFetcherRefusesRedirectsOffTheAllowlist(t *testing.T) {
	t.Parallel()

	var offHostHits atomic.Int32
	offHost := newTLSImageServer(t, func(w http.ResponseWriter, _ *http.Request) {
		offHostHits.Add(1)
		_, _ = w.Write([]byte("x"))
	})

	t.Run("to another host", func(t *testing.T) {
		t.Parallel()

		srv := newTLSImageServer(t, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, offHost.URL+"/a.png", http.StatusFound)
		})

		if _, err := newTestFetcher(srv).Fetch(t.Context(), srv.URL+"/a.png", 1024); err == nil {
			t.Fatal("Fetch followed a redirect to another host, want error")
		}
	})

	t.Run("downgrading to HTTP", func(t *testing.T) {
		t.Parallel()

		var srv *httptest.Server
		srv = newTLSImageServer(t, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://"+srv.Listener.Addr().String()+"/a.png", http.StatusFound)
		})

		if _, err := newTestFetcher(srv).Fetch(t.Context(), srv.URL+"/a.png", 1024); err == nil {
			t.Fatal("Fetch followed a redirect to HTTP, want error")
		}
	})

	t.Run("in a loop", func(t *testing.T) {
		t.Parallel()

		srv := newTLSImageServer(t, func(w http.ResponseWriter, r *http.Request) {
			//nolint:gosec // synthetic redirect loop to the same test server
			http.Redirect(w, r, "/loop", http.StatusFound)
		})

		if _, err := newTestFetcher(srv).Fetch(t.Context(), srv.URL+"/loop", 1024); err == nil {
			t.Fatal("Fetch followed an endless redirect loop, want error")
		}
	})

	t.Run("within the allowed host", func(t *testing.T) {
		t.Parallel()

		srv := newTLSImageServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/start" {
				http.Redirect(w, r, "/final.png", http.StatusFound)
				return
			}
			_, _ = w.Write([]byte("final"))
		})

		got, err := newTestFetcher(srv).Fetch(t.Context(), srv.URL+"/start", 1024)
		if err != nil || string(got) != "final" {
			t.Fatalf("Fetch = %q, %v, want the redirected body", got, err)
		}
	})

	t.Cleanup(func() {
		if got := offHostHits.Load(); got != 0 {
			t.Errorf("off-allowlist server received %d requests, want 0", got)
		}
	})
}

func TestHTTPImageFetcherErrorsNeverEchoTheURL(t *testing.T) {
	t.Parallel()

	srv := newTLSImageServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/secret-signature", http.StatusFound)
	})

	_, err := newTestFetcher(srv).Fetch(t.Context(), srv.URL+"/a.png?hm=secret-signature", 1024)
	if err == nil {
		t.Fatal("Fetch succeeded, want error")
	}
	if strings.Contains(err.Error(), "secret-signature") || strings.Contains(err.Error(), srv.Listener.Addr().String()) {
		t.Errorf("error %q echoes the URL", err.Error())
	}
}

func TestHTTPImageFetcherValidatesInput(t *testing.T) {
	t.Parallel()

	var nilFetcher *capitime.HTTPImageFetcher
	if _, err := nilFetcher.Fetch(t.Context(), "https://cdn.discordapp.com/a.png", 1024); err == nil {
		t.Error("nil fetcher succeeded, want error")
	}

	fetcher := capitime.NewHTTPImageFetcher(capitime.HTTPImageFetcherConfig{})
	if _, err := fetcher.Fetch(t.Context(), "https://cdn.discordapp.com/a.png", 0); err == nil {
		t.Error("non-positive limit succeeded, want error")
	}
	//nolint:staticcheck // testing nil context guard
	if _, err := fetcher.Fetch(nil, "https://cdn.discordapp.com/a.png", 1024); err == nil {
		t.Error("nil context succeeded, want error")
	}
}

func TestHTTPImageFetcherStopsWhenContextEnds(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	srv := newTLSImageServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		_, _ = w.Write([]byte("late"))
	})
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	if _, err := newTestFetcher(srv).Fetch(ctx, srv.URL+"/slow.png", 1024); err == nil {
		t.Fatal("Fetch succeeded after its context ended, want error")
	}
}
