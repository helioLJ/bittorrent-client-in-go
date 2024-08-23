package torrent

import (
	"bittorrent-client/internal/bencode"
	"bittorrent-client/pkg/logger"
	"crypto/sha1"
	"errors"
	"fmt"
	"log"
)

// InfoDict represents the 'info' dictionary in a torrent file
type InfoDict struct {
	PieceLength int64  `bencode:"piece length"`
	Pieces      string `bencode:"pieces"`
	Name        string `bencode:"name"`
	Length      int64  `bencode:"length,omitempty"`
	Files       []struct {
		Length int64    `bencode:"length"`
		Path   []string `bencode:"path"`
	} `bencode:"files,omitempty"`
}

// MetaInfo represents the metadata of a torrent file
type MetaInfo struct {
	Announce string   `bencode:"announce"`
	Info     InfoDict `bencode:"info"`
}

// ParseMetaInfo parses the metadata from torrent file content
func ParseMetaInfo(data []byte) (*MetaInfo, error) {
    var metaInfo MetaInfo
    err := bencode.Unmarshal(data, &metaInfo)
    if err != nil {
        return nil, fmt.Errorf("failed to unmarshal torrent file: %w", err)
    }

    // Debug logging to inspect the parsed data
    // log.Printf("Raw parsed MetaInfo: %+v", metaInfo)

    // Check the announce URL
    if metaInfo.Announce == "" {
        logger.Warn("Missing announce URL in torrent file")
        // Instead of returning an error, we'll log a warning and continue
    }

    // Additional checks for other required fields
    if metaInfo.Info.PieceLength == 0 {
        return nil, errors.New("missing or invalid piece length")
    }

    if len(metaInfo.Info.Pieces) == 0 {
        return nil, errors.New("missing pieces information")
    }

    if metaInfo.Info.Name == "" {
        return nil, errors.New("missing torrent name")
    }

    // Log more details about the parsed info
    log.Printf("Parsed Info: Name=%s, PieceLength=%d, PiecesLength=%d, Files=%v",
        metaInfo.Info.Name, metaInfo.Info.PieceLength, len(metaInfo.Info.Pieces), metaInfo.Info.Files)

    // If everything is valid, return the parsed MetaInfo
    return &metaInfo, nil
}

// InfoHash returns the SHA1 hash of the info dictionary
func (m *MetaInfo) InfoHash() ([20]byte, error) {
	infoDict := map[string]interface{}{
			"piece length": m.Info.PieceLength,
			"pieces":       m.Info.Pieces,
			"name":         m.Info.Name,
	}

	if m.Info.Length > 0 {
		infoDict["length"] = m.Info.Length
	} else {
		infoDict["files"] = m.Info.Files
	}

	encodedInfo, err := bencode.Encode(infoDict)
	if err != nil {
		return [20]byte{}, err
	}

	return sha1.Sum(encodedInfo), nil
}

// TotalLength returns the total length of all files in the torrent
func (m *MetaInfo) TotalLength() int64 {
    if m.Info.Length > 0 {
        return m.Info.Length
    }

    var total int64
    for _, file := range m.Info.Files {
        total += file.Length
    }
    return total
}

// NumPieces returns the number of pieces in the torrent
func (m *MetaInfo) NumPieces() int {
	return len(m.Info.Pieces) / 20
}

// PieceHash returns the hash of a specific piece
func (m *MetaInfo) PieceHash(index int) ([20]byte, error) {
	if index < 0 || index >= m.NumPieces() {
		return [20]byte{}, fmt.Errorf("invalid piece index: %d", index)
	}

	start := index * 20
	end := start + 20
	var hash [20]byte
	copy(hash[:], m.Info.Pieces[start:end])
	return hash, nil
}