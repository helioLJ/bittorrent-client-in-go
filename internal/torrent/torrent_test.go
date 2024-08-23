package torrent

import (
	"context"
	"net"
	"testing"
	"time"
)

type MockTracker struct{}

func (m *MockTracker) Announce(downloaded, uploaded, left int64) ([]net.Addr, error) {
	return []net.Addr{}, nil
}

func TestNewTorrent(t *testing.T) {
    metaInfo := &MetaInfo{
        Announce: "http://tracker.example.com/announce",
        Info: InfoDict{
            PieceLength: 262144,
            Pieces:      "1234567890abcdefghij1234567890abcdefghij",
            Name:        "example.txt",
            Length:      1048576,
        },
    }

	mockTracker := &MockTracker{}
	torrent, err := NewTorrent(metaInfo, mockTracker)
	if err != nil {
		t.Fatalf("NewTorrent failed: %v", err)
	}

	if torrent.MetaInfo != metaInfo {
		t.Errorf("NewTorrent did not set MetaInfo correctly")
	}

	if len(torrent.PeerID) != 20 {
		t.Errorf("NewTorrent did not generate a valid PeerID")
	}

	if len(torrent.Pieces) != 2 {
		t.Errorf("NewTorrent created %d pieces, want 2", len(torrent.Pieces))
	}

	if torrent.Left != 1048576 {
        t.Errorf("NewTorrent set Left to %d, want 1048576", torrent.Left)
    }
}

func TestDownload(t *testing.T) {
	metaInfo := &MetaInfo{
		Announce: "http://tracker.example.com/announce",
		Info: InfoDict{
			PieceLength: 262144,
			Pieces:      "1234567890abcdefghij1234567890abcdefghij",
			Name:        "example.txt",
			Length:      1048576,
		},
	}

	mockTracker := &MockTracker{}
	torrent, err := NewTorrent(metaInfo, mockTracker)
	if err != nil {
		t.Fatalf("NewTorrent failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = torrent.Download(ctx)
	if err != context.DeadlineExceeded {
		t.Errorf("Download returned unexpected error: %v", err)
	}
}