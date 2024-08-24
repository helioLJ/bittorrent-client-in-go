package tracker

import (
	"bittorrent-client/pkg/logger"
	"bittorrent-client/internal/bencode"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Tracker struct {
    URL           *url.URL
    PeerID        [20]byte
    InfoHash      [20]byte
    client        *http.Client
    udpConn       *net.UDPConn
    fallbackTrackers []*Tracker
}

type MultiTracker struct {
    trackers []*Tracker
    peerID   [20]byte
    infoHash [20]byte
}

func (t *Tracker) udpConnect() (uint64, error) {
    if t.udpConn == nil {
        return 0, errors.New("UDP connection not initialized")
    }

    connectReq := make([]byte, 16)
    binary.BigEndian.PutUint64(connectReq[0:8], 0x41727101980) // connection id
    binary.BigEndian.PutUint32(connectReq[8:12], 0)            // action (connect)
    binary.BigEndian.PutUint32(connectReq[12:16], rand.Uint32()) // transaction id

    // Set a timeout for the connection attempt
    err := t.udpConn.SetDeadline(time.Now().Add(15 * time.Second))
    if err != nil {
        return 0, err
    }
    defer t.udpConn.SetDeadline(time.Time{})

    _, err = t.udpConn.Write(connectReq)
    if err != nil {
        return 0, err
    }

    respBuf := make([]byte, 16)
    n, err := t.udpConn.Read(respBuf)
    if err != nil {
        return 0, err
    }
    if n != 16 {
        return 0, errors.New("invalid response length for connect")
    }

    action := binary.BigEndian.Uint32(respBuf[0:4])
    if action != 0 {
        return 0, errors.New("connect request failed")
    }

    transactionID := binary.BigEndian.Uint32(respBuf[4:8])
    if transactionID != binary.BigEndian.Uint32(connectReq[12:16]) {
        return 0, errors.New("transaction ID mismatch")
    }

    connectionID := binary.BigEndian.Uint64(respBuf[8:16])
    return connectionID, nil
}

func NewTracker(urlStr string, peerID [20]byte, infoHash [20]byte) (*Tracker, error) {
    u, err := url.Parse(urlStr)
    if err != nil {
        return nil, err
    }

    t := &Tracker{
        URL:      u,
        PeerID:   peerID,
        InfoHash: infoHash,
        client:   &http.Client{},
    }

    if u.Scheme == "udp" {
        // Initialize UDP connection
        addr, err := net.ResolveUDPAddr("udp", u.Host)
        if err != nil {
            return nil, err
        }
        t.udpConn, err = net.DialUDP("udp", nil, addr)
        if err != nil {
            return nil, err
        }
    }

    return t, nil
}

func NewMultiTracker(peerID [20]byte, infoHash [20]byte) *MultiTracker {
    return &MultiTracker{
        peerID:   peerID,
        infoHash: infoHash,
    }
}

func (mt *MultiTracker) AddTracker(t *Tracker) {
    mt.trackers = append(mt.trackers, t)
}

func (mt *MultiTracker) Announce(downloaded, uploaded, left int64) ([]net.Addr, error) {
    var allPeers []net.Addr
    var lastErr error

    for _, t := range mt.trackers {
        peers, err := t.Announce(downloaded, uploaded, left)
        if err != nil {
            logger.Warn("Failed to announce to tracker %s: %v", t.URL, err)
            lastErr = err
            continue
        }
        logger.Info("Received %d peers from tracker %s", len(peers), t.URL)
        allPeers = append(allPeers, peers...)
    }

    uniquePeers := uniquePeers(allPeers)
    logger.Info("Total unique peers received from all trackers: %d", len(uniquePeers))

    if len(uniquePeers) == 0 && lastErr != nil {
        return nil, lastErr
    }

    return uniquePeers, nil
}

// Add this helper function to remove duplicate peers
func uniquePeers(peers []net.Addr) []net.Addr {
    seen := make(map[string]bool)
    unique := []net.Addr{}
    for _, peer := range peers {
        if _, ok := seen[peer.String()]; !ok {
            seen[peer.String()] = true
            unique = append(unique, peer)
        }
    }
    return unique
}

func (t *Tracker) Announce(downloaded, uploaded, left int64) ([]net.Addr, error) {
    peers, err := t.announce(downloaded, uploaded, left)
    if err != nil {
        for _, fallback := range t.fallbackTrackers {
            peers, err = fallback.announce(downloaded, uploaded, left)
            if err == nil {
                logger.Info("Successfully announced to fallback tracker: %s", fallback.URL)
                return peers, nil
            }
        }
    } else {
        logger.Info("Successfully announced to tracker: %s", t.URL)
    }
    return peers, err
}

func (t *Tracker) announce(downloaded, uploaded, left int64) ([]net.Addr, error) {
    if t.URL.Scheme == "http" || t.URL.Scheme == "https" {
        return t.httpAnnounce(downloaded, uploaded, left)
    } else if t.URL.Scheme == "udp" {
        return t.udpAnnounce(downloaded, uploaded, left)
    }
    return nil, fmt.Errorf("unsupported tracker protocol: %s", t.URL.Scheme)
}

func (t *Tracker) httpAnnounce(downloaded, uploaded, left int64) ([]net.Addr, error) {
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

    logger.Debug("HTTP tracker URL: %s", u.String())

    resp, err := http.Get(u.String())
    if err != nil {
        logger.Error("HTTP tracker request failed: %v", err)
        return nil, err
    }
    defer resp.Body.Close()

    body, err := ioutil.ReadAll(resp.Body)
    if err != nil {
        logger.Error("Failed to read HTTP tracker response: %v", err)
        return nil, err
    }

    logger.Debug("HTTP tracker response length: %d bytes", len(body))

    decoded, err := decodeHttpResponse(body)
    if err != nil {
        return nil, err
    }

    decodedMap, ok := decoded.(map[string]interface{})
    if !ok {
        return nil, errors.New("invalid tracker response")
    }

    if decodedMap["failure reason"] != nil {
        return nil, fmt.Errorf("tracker returned failure: %v", decodedMap["failure reason"])
    }

    peersData, ok := decodedMap["peers"].(string)
    if !ok {
        return nil, errors.New("invalid peers data format")
    }

    peers, err := parsePeers([]byte(peersData))
    if err != nil {
        return nil, err
    }

    logger.Info("Received %d peers from HTTP tracker", len(peers))
    return peers, nil
}

// Simple decoder for HTTP tracker response
func decodeHttpResponse(body []byte) (interface{}, error) {
    var response interface{}
    err := bencode.Unmarshal(body, &response)
    if err != nil {
        return nil, err
    }
    return response, nil
}

func decodeDictionary(data []byte) (map[string]interface{}, []byte, error) {
    result := make(map[string]interface{})
    for len(data) > 0 && data[0] != 'e' {
        key, rest, err := decodeString(data)
        if err != nil {
            return nil, nil, err
        }

        value, rest, err := decodeValue(rest)
        if err != nil {
            return nil, nil, err
        }

        result[key] = value
        data = rest
    }

    if len(data) == 0 || data[0] != 'e' {
        return nil, nil, errors.New("invalid dictionary")
    }

    return result, data[1:], nil
}

func decodeString(data []byte) (string, []byte, error) {
    i := 0
    for i < len(data) && data[i] != ':' {
        i++
    }

    if i == len(data) {
        return "", nil, errors.New("invalid string")
    }

    length, err := strconv.Atoi(string(data[:i]))
    if err != nil {
        return "", nil, err
    }

    if i+1+length > len(data) {
        return "", nil, errors.New("string too short")
    }

    return string(data[i+1 : i+1+length]), data[i+1+length:], nil
}

func decodeValue(data []byte) (interface{}, []byte, error) {
    if len(data) == 0 {
        return nil, nil, errors.New("empty data")
    }

    switch data[0] {
    case 'i':
        return decodeInteger(data[1:])
    case 'l':
        return decodeList(data[1:])
    case 'd':
        return decodeDictionary(data[1:])
    default:
        return decodeString(data)
    }
}

func decodeInteger(data []byte) (int64, []byte, error) {
    i := 0
    for i < len(data) && data[i] != 'e' {
        i++
    }

    if i == len(data) {
        return 0, nil, errors.New("invalid integer")
    }

    n, err := strconv.ParseInt(string(data[:i]), 10, 64)
    if err != nil {
        return 0, nil, err
    }

    return n, data[i+1:], nil
}

func decodeList(data []byte) ([]interface{}, []byte, error) {
    var result []interface{}
    for len(data) > 0 && data[0] != 'e' {
        value, rest, err := decodeValue(data)
        if err != nil {
            return nil, nil, err
        }
        result = append(result, value)
        data = rest
    }

    if len(data) == 0 || data[0] != 'e' {
        return nil, nil, errors.New("invalid list")
    }

    return result, data[1:], nil
}

// In the udpAnnounce method, add more retries and better error handling
func (t *Tracker) udpAnnounce(downloaded, uploaded, left int64) ([]net.Addr, error) {
    maxRetries := 3
    for i := 0; i < maxRetries; i++ {
        connectionID, err := t.udpConnect()
        if err != nil {
            logger.Warn("UDP connect attempt %d failed: %v", i+1, err)
            time.Sleep(time.Second * time.Duration(i+1))
            continue
        }

        peers, err := t.udpAnnounceRequest(connectionID, downloaded, uploaded, left)
        if err == nil {
            return peers, nil
        }
        logger.Warn("UDP announce attempt %d failed: %v", i+1, err)
        time.Sleep(time.Second * time.Duration(i+1))
    }
    return nil, fmt.Errorf("all UDP announce attempts failed")
}

func (t *Tracker) udpAnnounceWithTimeout(downloaded, uploaded, left int64) ([]net.Addr, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    resultChan := make(chan []net.Addr, 1)
    errChan := make(chan error, 1)

    go func() {
        connectionID, err := t.udpConnect()
        if err != nil {
            errChan <- fmt.Errorf("UDP connect failed: %w", err)
            return
        }

        peers, err := t.udpAnnounceRequest(connectionID, downloaded, uploaded, left)
        if err != nil {
            errChan <- err
        } else {
            resultChan <- peers
        }
    }()

    select {
    case peers := <-resultChan:
        return peers, nil
    case err := <-errChan:
        return nil, err
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}

func (t *Tracker) udpAnnounceRequest(connectionID uint64, downloaded, uploaded, left int64) ([]net.Addr, error) {
    transactionID := rand.Uint32()
    announceReq := make([]byte, 98)

    binary.BigEndian.PutUint64(announceReq[0:8], connectionID)
    binary.BigEndian.PutUint32(announceReq[8:12], 1) // action (announce)
    binary.BigEndian.PutUint32(announceReq[12:16], transactionID)
    copy(announceReq[16:36], t.InfoHash[:])
    copy(announceReq[36:56], t.PeerID[:])
    binary.BigEndian.PutUint64(announceReq[56:64], uint64(downloaded))
    binary.BigEndian.PutUint64(announceReq[64:72], uint64(left))
    binary.BigEndian.PutUint64(announceReq[72:80], uint64(uploaded))
    binary.BigEndian.PutUint32(announceReq[80:84], 0) // event
    binary.BigEndian.PutUint32(announceReq[84:88], 0) // IP address (default)
    binary.BigEndian.PutUint32(announceReq[88:92], rand.Uint32()) // key
    binary.BigEndian.PutUint32(announceReq[92:96], ^uint32(0))    // num_want (-1, which means as many as possible)
    binary.BigEndian.PutUint16(announceReq[96:98], 6881) // port

    logger.Debug("Sending UDP announce request")

    err := t.udpConn.SetReadDeadline(time.Now().Add(30 * time.Second))
    if err != nil {
        return nil, err
    }

    _, err = t.udpConn.Write(announceReq)
    if err != nil {
        logger.Error("Failed to send UDP announce request: %v", err)
        return nil, err
    }

    respBuf := make([]byte, 1500)
    n, err := t.udpConn.Read(respBuf)
    if err != nil {
        logger.Error("Failed to read UDP announce response: %v", err)
        return nil, err
    }
    if n < 20 {
        logger.Error("Invalid UDP announce response length: %d bytes", n)
        return nil, errors.New("invalid response length for announce")
    }

    logger.Debug("Received UDP announce response: %d bytes", n)

    action := binary.BigEndian.Uint32(respBuf[0:4])
    respTransactionID := binary.BigEndian.Uint32(respBuf[4:8])

    if action != 1 {
        return nil, errors.New("announce request failed")
    }
    if respTransactionID != transactionID {
        return nil, errors.New("transaction ID mismatch")
    }

    interval := binary.BigEndian.Uint32(respBuf[8:12])
    leechers := binary.BigEndian.Uint32(respBuf[12:16])
    seeders := binary.BigEndian.Uint32(respBuf[16:20])

    logger.Debug("UDP announce response parsed successfully")

    // Log the tracker statistics including the interval
    logger.Info("Tracker stats: interval=%d, seeders=%d, leechers=%d", interval, seeders, leechers)

    // Parse peers
    peersBin := respBuf[20:n]
    logger.Debug("Raw peers data length: %d bytes", len(peersBin))

    parsedPeers, err := parsePeers(peersBin)
    if err != nil {
        logger.Error("Failed to parse peers: %v", err)
        return nil, err
    }

    logger.Info("Received %d peers from UDP tracker", len(parsedPeers))
    return parsedPeers, nil
}

func parsePeers(peersBin []byte) ([]net.Addr, error) {
    const peerSize = 6 // 4 for IP, 2 for port
    peerCount := len(peersBin) / peerSize

    peers := make([]net.Addr, 0, peerCount)

    for i := 0; i < len(peersBin); i += peerSize {
        if i+peerSize > len(peersBin) {
            break
        }

        ip := net.IP(peersBin[i : i+4])
        port := binary.BigEndian.Uint16(peersBin[i+4 : i+6])

        addr := &net.TCPAddr{
            IP:   ip,
            Port: int(port),
        }
        peers = append(peers, addr)
    }

    return peers, nil
}

// AddFallbackTracker adds a fallback tracker to the Tracker instance
func (t *Tracker) AddFallbackTracker(fallback *Tracker) {
    t.fallbackTrackers = append(t.fallbackTrackers, fallback)
}

type TrackerInterface interface {
    Announce(downloaded, uploaded, left int64) ([]net.Addr, error)
}
