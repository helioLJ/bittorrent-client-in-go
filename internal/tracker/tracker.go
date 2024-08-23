package tracker

import (
	"encoding/binary"
	"errors"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"bittorrent-client/internal/bencode"
)

// Tracker represents a BitTorrent tracker
type Tracker struct {
	URL      *url.URL
	PeerID   [20]byte
	InfoHash [20]byte
}

// NewTracker creates a new Tracker instance
func NewTracker(trackerURL *url.URL, peerID [20]byte, infoHash [20]byte) *Tracker {
	return &Tracker{
		URL:      trackerURL,
		PeerID:   peerID,
		InfoHash: infoHash,
	}
}

// Announce sends an announce request to the tracker
func (t *Tracker) Announce(downloaded, uploaded, left int64) ([]net.Addr, error) {
	u := *t.URL
	q := u.Query()
	q.Set("info_hash", string(t.InfoHash[:]))
	q.Set("peer_id", string(t.PeerID[:]))
	q.Set("port", "6881")
	q.Set("uploaded", strconv.FormatInt(uploaded, 10))
	q.Set("downloaded", strconv.FormatInt(downloaded, 10))
	q.Set("left", strconv.FormatInt(left, 10))
	q.Set("compact", "1")
	u.RawQuery = q.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var trackerResp struct {
		Peers string `bencode:"peers"`
	}

	decoded, err := bencode.Decode(body)
	if err != nil {
		return nil, err
	}

	decodedMap, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid tracker response")
	}

	peers, ok := decodedMap["peers"].(string)
	if !ok {
		return nil, errors.New("invalid peers in tracker response")
	}
	trackerResp.Peers = peers

	return parsePeers([]byte(trackerResp.Peers))
}

func parsePeers(peersBin []byte) ([]net.Addr, error) {
	const peerSize = 6 // 4 for IP, 2 for port
	numPeers := len(peersBin) / peerSize
	if len(peersBin)%peerSize != 0 {
		return nil, errors.New("received invalid peers")
	}

	peers := make([]net.Addr, numPeers)
	for i := 0; i < numPeers; i++ {
		offset := i * peerSize
		ip := net.IP(peersBin[offset : offset+4])
		port := binary.BigEndian.Uint16(peersBin[offset+4 : offset+6])
		peers[i] = &net.TCPAddr{IP: ip, Port: int(port)}
	}

	return peers, nil
}