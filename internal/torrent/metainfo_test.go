package torrent

import (
	"bittorrent-client/internal/bencode"
	"reflect"
	"testing"
)

func TestParseMetaInfo(t *testing.T) {
    torrentData := map[string]interface{}{
        "announce": "http://tracker.example.com/announce",
        "info": map[string]interface{}{
            "piece length": int64(262144),
            "pieces":       "1234567890abcdefghij1234567890abcdefghij",
            "name":         "example.txt",
            "length":       int64(1048576),
        },
    }

    encodedData, err := bencode.Encode(torrentData)
    if err != nil {
        t.Fatalf("Failed to encode test data: %v", err)
    }

    metainfo, err := ParseMetaInfo(encodedData)
    if err != nil {
        t.Fatalf("ParseMetaInfo failed: %v", err)
    }

    expectedMetaInfo := &MetaInfo{
        Announce: "http://tracker.example.com/announce",
        Info: InfoDict{
            PieceLength: 262144,
            Pieces:      "1234567890abcdefghij1234567890abcdefghij",
            Name:        "example.txt",
            Length:      1048576,
        },
    }

    if !reflect.DeepEqual(metainfo, expectedMetaInfo) {
        t.Errorf("ParseMetaInfo returned %+v, want %+v", metainfo, expectedMetaInfo)
    }
}

func TestInfoHash(t *testing.T) {
	metainfo := &MetaInfo{
		Announce: "http://tracker.example.com/announce",
		Info: InfoDict{
			PieceLength: 262144,
			Pieces:      "1234567890abcdefghij1234567890abcdefghij",
			Name:        "example.txt",
			Length:      1048576,
		},
	}

	hash, err := metainfo.InfoHash()
	if err != nil {
		t.Fatalf("InfoHash failed: %v", err)
	}

	if len(hash) != 20 {
		t.Errorf("InfoHash returned hash of length %d, want 20", len(hash))
	}

	// Check if the hash is non-zero
	zeroHash := [20]byte{}
	if hash == zeroHash {
		t.Errorf("InfoHash returned zero hash, expected non-zero hash")
	}

	// Run InfoHash again to check for consistency
	hash2, err := metainfo.InfoHash()
	if err != nil {
		t.Fatalf("Second InfoHash call failed: %v", err)
	}

	if hash != hash2 {
		t.Errorf("InfoHash is not consistent. First call: %x, Second call: %x", hash, hash2)
	}
}

func TestTotalLength(t *testing.T) {
	metainfo := &MetaInfo{
		Info: InfoDict{
			Length: 1048576,
		},
	}

	if length := metainfo.TotalLength(); length != 1048576 {
		t.Errorf("TotalLength returned %d, want 1048576", length)
	}

	metainfo.Info.Length = 0
	metainfo.Info.Files = []File{
		{Length: 524288, Path: []string{"part1"}},
		{Length: 524288, Path: []string{"part2"}},
	}

	if length := metainfo.TotalLength(); length != 1048576 {
		t.Errorf("TotalLength returned %d, want 1048576", length)
	}
}

func TestNumPieces(t *testing.T) {
	metainfo := &MetaInfo{
		Info: InfoDict{
			Pieces: "1234567890abcdefghij1234567890abcdefghij",
		},
	}

	if numPieces := metainfo.NumPieces(); numPieces != 2 {
		t.Errorf("NumPieces returned %d, want 2", numPieces)
	}
}

func TestPieceHash(t *testing.T) {
	metainfo := &MetaInfo{
		Info: InfoDict{
			Pieces: "1234567890abcdefghij1234567890abcdefghij",
		},
	}

	expectedHash := [20]byte{'1', '2', '3', '4', '5', '6', '7', '8', '9', '0', 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j'}

	hash, err := metainfo.PieceHash(0)
	if err != nil {
		t.Fatalf("PieceHash failed: %v", err)
	}

	if hash != expectedHash {
		t.Errorf("PieceHash returned %v, want %v", hash, expectedHash)
	}

	_, err = metainfo.PieceHash(2)
	if err == nil {
		t.Error("PieceHash should return an error for invalid index")
	}
}