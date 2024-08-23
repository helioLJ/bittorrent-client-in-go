package tracker

import (
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
    "bittorrent-client/pkg/logger"
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
        if err == nil {
            t.udpConn, _ = net.DialUDP("udp", nil, addr)
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

    logger.Info("Total unique peers received from all trackers: %d", len(uniquePeers(allPeers)))

    if len(allPeers) == 0 && lastErr != nil {
        return nil, lastErr
    }

    return uniquePeers(allPeers), nil
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

    peers, ok := decodedMap["peers"].(string)
    if !ok {
        logger.Error("Invalid peers in HTTP tracker response")
        return nil, errors.New("invalid peers in tracker response")
    }

    logger.Debug("Raw peers data length: %d bytes", len(peers))

    parsedPeers, err := parsePeers([]byte(peers))
    if err != nil {
        logger.Error("Failed to parse peers: %v", err)
        return nil, err
    }

    logger.Info("Received %d peers from HTTP tracker", len(parsedPeers))
    return parsedPeers, nil
}

// Simple decoder for HTTP tracker response
func decodeHttpResponse(data []byte) (interface{}, error) {
    if len(data) == 0 {
        return nil, errors.New("empty response")
    }

    if data[0] == 'd' {
        dict, _, err := decodeDictionary(data[1:])
        return dict, err
    }

    return nil, errors.New("unsupported bencode type")
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

func (t *Tracker) udpAnnounce(downloaded, uploaded, left int64) ([]net.Addr, error) {
    if t.udpConn == nil {
        return nil, errors.New("UDP connection not initialized")
    }

    // Step 1: Connect
    connectionID, err := t.udpConnect()
    if err != nil {
        return nil, fmt.Errorf("UDP connect failed: %w", err)
    }

    // Step 2: Announce
    return t.udpAnnounceRequest(connectionID, downloaded, uploaded, left)
}

func (t *Tracker) udpConnect() (uint64, error) {
    transactionID := rand.Uint32()
    connectReq := make([]byte, 16)
    binary.BigEndian.PutUint64(connectReq[0:8], 0x41727101980) // connection id
    binary.BigEndian.PutUint32(connectReq[8:12], 0)            // action (connect)
    binary.BigEndian.PutUint32(connectReq[12:16], transactionID)

    err := t.udpConn.SetDeadline(time.Now().Add(15 * time.Second))
    if err != nil {
        return 0, err
    }

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
    respTransactionID := binary.BigEndian.Uint32(respBuf[4:8])
    connectionID := binary.BigEndian.Uint64(respBuf[8:16])

    if action != 0 {
        return 0, errors.New("connect request failed")
    }
    if respTransactionID != transactionID {
        return 0, errors.New("transaction ID mismatch")
    }

    return connectionID, nil
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

    err := t.udpConn.SetDeadline(time.Now().Add(15 * time.Second))
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
    numPeers := len(peersBin) / peerSize
    if len(peersBin)%peerSize != 0 {
        logger.Error("Received invalid peers data: length %d is not a multiple of %d", len(peersBin), peerSize)
        return nil, errors.New("received invalid peers")
    }

    peers := make([]net.Addr, numPeers)
    for i := 0; i < numPeers; i++ {
        offset := i * peerSize
        ip := net.IP(peersBin[offset : offset+4])
        port := binary.BigEndian.Uint16(peersBin[offset+4 : offset+6])
        peers[i] = &net.TCPAddr{IP: ip, Port: int(port)}
        logger.Debug("Parsed peer %d: %s:%d", i+1, ip, port)
    }

    logger.Info("Successfully parsed %d peers", numPeers)
    return peers, nil
}

// AddFallbackTracker adds a fallback tracker to the Tracker instance
func (t *Tracker) AddFallbackTracker(fallback *Tracker) {
    t.fallbackTrackers = append(t.fallbackTrackers, fallback)
}

type TrackerInterface interface {
    Announce(downloaded, uploaded, left int64) ([]net.Addr, error)
}
