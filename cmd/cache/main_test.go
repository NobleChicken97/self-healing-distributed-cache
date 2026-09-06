package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"selfhealingcache/internal/ring"
	"selfhealingcache/internal/server"
	"selfhealingcache/internal/store"
)

func TestGossipBindAddr(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want string
	}{
		{"empty host binds all interfaces", ":8080", "0.0.0.0"},
		{"explicit localhost stays local", "127.0.0.1:8081", "127.0.0.1"},
		{"explicit host kept", "10.0.0.5:8080", "10.0.0.5"},
		{"garbage binds all interfaces", "not-an-addr", "0.0.0.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gossipBindAddr(tt.addr); got != tt.want {
				t.Errorf("gossipBindAddr(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestPeersToGossipPeers(t *testing.T) {
	tests := []struct {
		name       string
		peers      string
		gossipPort int
		useDefault bool
		want       []string
	}{
		{"empty", "", 7946, false, nil},
		{"explicit uniform port", "10.0.0.2:8080,10.0.0.3:8080", 7946, false,
			[]string{"10.0.0.2:7946", "10.0.0.3:7946"}},
		{"local shorthand means localhost", ":8081,:8082", 7946, false,
			[]string{"127.0.0.1:7946", "127.0.0.1:7946"}},
		{"custom gossip port", "10.0.0.2:8080", 9080, false, []string{"10.0.0.2:9080"}},
		{"blank entries skipped", "10.0.0.2:8080, ,10.0.0.3:8080", 7946, false,
			[]string{"10.0.0.2:7946", "10.0.0.3:7946"}},
		{"default convention derives peer HTTP+1000", "127.0.0.1:18081,127.0.0.1:18082", 19080, true,
			[]string{"127.0.0.1:19081", "127.0.0.1:19082"}},
		{"default convention with shorthand", ":8081", 9080, true,
			[]string{"127.0.0.1:9081"}},
		{"default convention bare host falls back", "cache-node", 7946, true,
			[]string{"cache-node:7946"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := peersToGossipPeers(tt.peers, tt.gossipPort, tt.useDefault); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("peersToGossipPeers(%q, %d, %v) = %v, want %v", tt.peers, tt.gossipPort, tt.useDefault, got, tt.want)
			}
		})
	}
}

// TestDashboardServes verifies the embedded operations dashboard is served
// at / with API routes keeping precedence (a GET on /set must answer 405
// from the API, not 404 from the file server).
func TestDashboardServes(t *testing.T) {
	st := store.New(time.Second)
	defer st.Close()
	r := ring.New(10)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:19301"})
	srv := server.New(st, "node-a", r)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	get := func(path string) (int, string, string) {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s failed: %v", path, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), string(body)
	}

	code, ctype, body := get("/")
	if code != http.StatusOK || !strings.Contains(ctype, "text/html") {
		t.Fatalf("/ -> %d %q, want 200 text/html", code, ctype)
	}
	for _, want := range []string{"SHDC", "/css/style.css", "/js/app.js", "id=\"meshSvg\""} {
		if !strings.Contains(body, want) {
			t.Fatalf("/ missing %q", want)
		}
	}
	if code, _, _ := get("/css/style.css"); code != http.StatusOK {
		t.Fatalf("/css/style.css -> %d, want 200", code)
	}
	if code, _, _ := get("/js/app.js"); code != http.StatusOK {
		t.Fatalf("/js/app.js -> %d, want 200", code)
	}
	if code, _, _ := get("/no-such-dashboard-page"); code != http.StatusNotFound {
		t.Fatalf("unknown path -> %d, want 404", code)
	}
	if code, _, _ := get("/set"); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /set -> %d, want 405 from API (route precedence)", code)
	}
}
