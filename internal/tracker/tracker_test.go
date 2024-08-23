package tracker

// import (
// 	"net"
// 	"net/http"
// 	"net/http/httptest"
// 	"net/url"
// 	"testing"

// 	"bittorrent-client/internal/bencode"
// )

// func TestTracker(t *testing.T) {
// 	// Start a mock tracker server
// 	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		response := map[string]interface{}{
// 			"interval": 1800,
// 			"peers":    "\x7f\x00\x00\x01\x1a\xe1\x7f\x00\x00\x01\x1a\xe2", // 127.0.0.1:6881, 127.0.0.1:6882
// 		}
// 		encoded, err := bencode.Encode(response)
// 		if err != nil {
// 			t.Fatalf("Failed to encode response: %v", err)
// 		}
// 		w.Write(encoded)
// 	}))
// 	defer server.Close()

// 	trackerURL, err := url.Parse(server.URL)
// 	if err != nil {
// 		t.Fatalf("Failed to parse server URL: %v", err)
// 	}

// 	peerID := [20]byte{1, 2, 3, 4, 5}
// 	infoHash := [20]byte{6, 7, 8, 9, 10}
// 	tracker := NewTracker(trackerURL, peerID, infoHash)
// 		// Test Announce
// 		peers, err := tracker.Announce(0, 0, 1000)
// 		if err != nil {
// 			t.Fatalf("Announce failed: %v", err)
// 		}
	
// 		if len(peers) != 2 {
// 			t.Fatalf("Expected 2 peers, got %d", len(peers))
// 		}
	
// 		expectedPeers := []net.Addr{
// 			&net.TCPAddr{IP: net.IP{127, 0, 0, 1}, Port: 6881},
// 			&net.TCPAddr{IP: net.IP{127, 0, 0, 1}, Port: 6882},
// 		}
	
// 		for i, peer := range peers {
// 			if peer.String() != expectedPeers[i].String() {
// 				t.Errorf("Expected peer %s, got %s", expectedPeers[i], peer)
// 			}
// 		}
// 	}