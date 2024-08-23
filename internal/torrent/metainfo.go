package torrent

import (
	"bittorrent-client/internal/bencode"
	"crypto/sha1"
	"errors"
	"fmt"
	"strconv"
)

// MetaInfo represents the metadata of a torrent file
type MetaInfo struct {
	Announce string
	Info     InfoDict
}

// InfoDict contains information about the files in the torrent
type InfoDict struct {
    PieceLength int64    `bencode:"piece length"`
    Pieces      string   `bencode:"pieces"`
    Name        string   `bencode:"name"`
    Length      int64    `bencode:"length,omitempty"`
    Files       []File   `bencode:"files,omitempty"`
}

// File represents a file in a multi-file torrent
type File struct {
    Length int64    `bencode:"length"`
    Path   []string `bencode:"path"`
}

// ParseMetaInfo parses a .torrent file
func ParseMetaInfo(data []byte) (*MetaInfo, error) {
    var torrent map[string]interface{}
    decoded, err := bencode.Decode(data)
    if err != nil {
        return nil, err
    }

    var ok bool
    torrent, ok = decoded.(map[string]interface{})
    if !ok {
        return nil, errors.New("invalid torrent structure")
    }

    metainfo := &MetaInfo{}

    announce, ok := torrent["announce"].(string)
    if !ok {
        return nil, errors.New("invalid announce URL")
    }
    metainfo.Announce = announce

    infoDict, ok := torrent["info"].(map[string]interface{})
    if !ok {
        return nil, errors.New("invalid info dictionary")
    }

    pieceLength, ok := infoDict["piece length"].(int64)
    if !ok {
        // Try to parse it as a string
        pieceLength, err = strconv.ParseInt(fmt.Sprint(infoDict["piece length"]), 10, 64)
        if err != nil {
            return nil, errors.New("invalid piece length")
        }
    }
    metainfo.Info.PieceLength = pieceLength

    pieces, ok := infoDict["pieces"].(string)
    if !ok {
        return nil, errors.New("invalid pieces")
    }
    metainfo.Info.Pieces = pieces

    name, ok := infoDict["name"].(string)
    if !ok {
        return nil, errors.New("invalid name")
    }
    metainfo.Info.Name = name

    if length, ok := infoDict["length"].(int64); ok {
        metainfo.Info.Length = length
    } else if files, ok := infoDict["files"].([]interface{}); ok {
        for _, file := range files {
            fileMap, ok := file.(map[string]interface{})
            if !ok {
                return nil, errors.New("invalid file entry")
            }
            fileLength, ok := fileMap["length"].(int64)
            if !ok {
                return nil, errors.New("invalid file length")
            }
            filePath, ok := fileMap["path"].([]interface{})
            if !ok {
                return nil, errors.New("invalid file path")
            }
            pathStrings := make([]string, len(filePath))
            for i, p := range filePath {
                pathStrings[i], ok = p.(string)
                if !ok {
                    return nil, errors.New("invalid file path element")
                }
            }
            metainfo.Info.Files = append(metainfo.Info.Files, File{Length: fileLength, Path: pathStrings})
        }
    } else {
        return nil, errors.New("invalid torrent structure: missing length or files")
    }

    return metainfo, nil
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