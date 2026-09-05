package main

import (
	"reflect"
	"testing"
)

func TestPeersToGossipPeers(t *testing.T) {
	tests := []struct {
		name       string
		peers      string
		gossipPort int
		want       []string
	}{
		{"empty", "", 7946, nil},
		{"http peers retargeted to gossip port", "10.0.0.2:8080,10.0.0.3:8080", 7946,
			[]string{"10.0.0.2:7946", "10.0.0.3:7946"}},
		{"local shorthand means localhost", ":8081,:8082", 7946,
			[]string{"127.0.0.1:7946", "127.0.0.1:7946"}},
		{"custom gossip port", "10.0.0.2:8080", 9080, []string{"10.0.0.2:9080"}},
		{"blank entries skipped", "10.0.0.2:8080, ,10.0.0.3:8080", 7946,
			[]string{"10.0.0.2:7946", "10.0.0.3:7946"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := peersToGossipPeers(tt.peers, tt.gossipPort); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("peersToGossipPeers(%q, %d) = %v, want %v", tt.peers, tt.gossipPort, got, tt.want)
			}
		})
	}
}
