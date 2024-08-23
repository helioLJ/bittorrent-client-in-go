package torrent

import (
	"bittorrent-client/internal/file"
	"bittorrent-client/internal/peer"
	"bittorrent-client/pkg/logger"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/torrent/metainfo"
)

type TrackerInterface interface {
	Announce(downloaded, uploaded, left int64) ([]net.Addr, error)
}

// Torrent represents a torrent file and its download state
type Torrent struct {
	MetaInfo    *MetaInfo
	PeerID      [20]byte
	Peers       []*peer.Peer
	Pieces      []Piece
	Tracker     TrackerInterface
	Downloaded  int64
	Uploaded    int64
	Left        int64
	ActivePeers int
	peerLimiter *rate.Limiter
	activePeersMutex sync.Mutex
	failedPeers     map[string]time.Time
	failedPeersMutex sync.Mutex
	FileManager      *file.FileManager
	dhtNode          *dht.Server
	startTime        time.Time
}

// Piece represents a piece of the torrent
type Piece struct {
	Index     int
	Hash      [20]byte
	Length    int64
	Data      []byte
	Downloaded bool
}

// MagnetInfo represents the metadata extracted from a magnet link
type MagnetInfo struct {
    InfoHash    [20]byte
    DisplayName string
    Trackers    []string
}

// ParseMagnetLink parses a magnet link and returns the MagnetInfo
func ParseMagnetLink(magnet string) (*MagnetInfo, error) {
    u, err := url.Parse(magnet)
    if err != nil {
        return nil, err
    }

    params := u.Query()

    infoHash, err := hex.DecodeString(strings.TrimPrefix(params.Get("xt"), "urn:btih:"))
    if err != nil {
        return nil, err
    }

    displayName, err := url.QueryUnescape(params.Get("dn"))
    if err != nil {
        return nil, err
    }

    trackers := params["tr"]

    magnetInfo := &MagnetInfo{
        InfoHash:    [20]byte{},
        DisplayName: displayName,
        Trackers:    trackers,
    }

    copy(magnetInfo.InfoHash[:], infoHash)

    return magnetInfo, nil
}

// ConvertToMetaInfo creates a partial MetaInfo from MagnetInfo
func (m *MagnetInfo) ConvertToMetaInfo() *MetaInfo {
    announce := ""
    if len(m.Trackers) > 0 {
        announce = m.Trackers[0]
    }

    return &MetaInfo{
        Announce: announce,
        Info: InfoDict{
            Name:        m.DisplayName,
            Pieces:      string(m.InfoHash[:]), // Store InfoHash in Pieces field temporarily
            PieceLength: 256 * 1024,            // Set a default piece length (e.g., 256 KiB)
        },
    }
}

// NewTorrent creates a new Torrent instance
func NewTorrent(metaInfo *MetaInfo, tracker TrackerInterface) (*Torrent, error) {
    peerID := generatePeerID()
    var pieces []Piece

    if metaInfo.Info.PieceLength > 0 {
        pieces = make([]Piece, metaInfo.NumPieces())
        for i := 0; i < len(pieces); i++ {
            hash, err := metaInfo.PieceHash(i)
            if err != nil {
                return nil, err
            }
            pieces[i] = Piece{
                Index:  i,
                Hash:   hash,
                Length: calculatePieceLength(metaInfo, i),
            }
        }
    }

    t := &Torrent{
        MetaInfo:    metaInfo,
        PeerID:      peerID,
        Pieces:      pieces,
        Left:        metaInfo.TotalLength(),
        Tracker:     tracker,
        peerLimiter: rate.NewLimiter(rate.Every(100*time.Millisecond), 1), // Limit new connections to 10 per second
        failedPeers: make(map[string]time.Time),
        FileManager:  nil, // This will be set later using SetFileManager
        startTime:   time.Now(),
    }

    return t, nil
}

// Download starts downloading the torrent
func (t *Torrent) Download(ctx context.Context) error {
    logger.Info("Starting torrent download")

    // Start the peer connection manager
    go t.managePeerConnections(ctx)

    // Initialize DHT
    if err := t.UseDHT(ctx); err != nil {
        logger.Warn("Failed to initialize DHT: %v", err)
    }

    // Announce to the tracker and get peers
    peers, err := t.Tracker.Announce(t.Downloaded, t.Uploaded, t.Left)
    if err != nil {
        logger.Warn("Tracker announce failed: %v. Falling back to DHT.", err)
    } else {
        // Connect to peers from tracker
        for _, addr := range peers {
            go t.connectToPeers(ctx, addr)
        }
    }

    // Main download loop
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            logger.Info("Download cancelled")
            return ctx.Err()
        case <-ticker.C:
            t.activePeersMutex.Lock()
            activePeers := t.ActivePeers
            t.activePeersMutex.Unlock()
            
            t.failedPeersMutex.Lock()
            failedPeersCount := len(t.failedPeers)
            t.failedPeersMutex.Unlock()
            
            logger.Info("Status: Active peers: %d, Failed peers: %d, Downloaded: %d/%d", 
                activePeers, failedPeersCount, t.Downloaded, t.MetaInfo.TotalLength())

            // Check if download is complete
            if t.isComplete() {
                logger.Info("Download complete")
                err := t.verifyDownload()
                if err != nil {
                    logger.Error("Download verification failed: %v", err)
                    return err
                }
                return nil
            }

            // Request more peers if needed
            if activePeers < 10 {
                go t.requestMorePeers(ctx)
            }

            // Check download progress
            progress := float64(t.Downloaded) / float64(t.MetaInfo.TotalLength()) * 100
            logger.Info("Download progress: %.2f%%", progress)

            // Implement a timeout mechanism
            if t.Downloaded == 0 && time.Since(t.startTime) > 5*time.Minute {
                logger.Warn("No progress after 5 minutes, restarting download process")
                return ErrDownloadTimeout
            }
        }
    }
}

// Add this error to your package
var ErrDownloadTimeout = errors.New("download timed out")

func (t *Torrent) isComplete() bool {
    for _, piece := range t.Pieces {
        if !piece.Downloaded {
            return false
        }
    }
    return true
}

func (t *Torrent) verifyDownload() error {
    logger.Info("Verifying downloaded data...")
    for i, piece := range t.Pieces {
        data, err := t.FileManager.ReadPiece(i, 0, piece.Length)
        if err != nil {
            return fmt.Errorf("failed to read piece %d: %v", i, err)
        }
        if !t.verifyPieceHash(i, data) {
            return fmt.Errorf("piece %d failed verification", i)
        }
    }
    logger.Info("All pieces verified successfully")
    return nil
}

func (t *Torrent) managePeerConnections(ctx context.Context) {
    ticker := time.NewTicker(1 * time.Minute)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            t.activePeersMutex.Lock()
            activePeers := t.ActivePeers
            t.activePeersMutex.Unlock()

            if activePeers < 10 {
                logger.Info("Active peers below threshold, requesting more peers")
                go t.requestMorePeers(ctx)
            }

            // Clean up old failed peers
            t.failedPeersMutex.Lock()
            for addr, lastFailure := range t.failedPeers {
                if time.Since(lastFailure) > 30*time.Minute {
                    delete(t.failedPeers, addr)
                    logger.Info("Removed %s from failed peers list", addr)
                }
            }
            t.failedPeersMutex.Unlock()

            logger.Info("Peer connection status: Active peers: %d, Failed peers: %d", activePeers, len(t.failedPeers))
        }
    }
}

func (t *Torrent) handlePeer(ctx context.Context, p *peer.Peer) {
    defer func() {
        p.Disconnect()
        t.activePeersMutex.Lock()
        t.ActivePeers--
        t.activePeersMutex.Unlock()
    }()

    idleTimeout := 2 * time.Minute
    timer := time.NewTimer(idleTimeout)
    defer timer.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-timer.C:
            logger.Info("Peer %s idle for too long, disconnecting", p.Addr)
            return
        default:
            msgType, payload, err := p.ReadMessage()
            if err != nil {
                if err == io.EOF {
                    logger.Debug("Peer %s disconnected", p.Addr)
                } else {
                    logger.Error("Error reading message from peer %s: %v", p.Addr, err)
                }
                return
            }

            if !timer.Stop() {
                <-timer.C
            }
            timer.Reset(idleTimeout)

            switch msgType {
            case peer.MsgChoke:
                p.Choked = true
                logger.Debug("Peer %s choked us", p.Addr)

            case peer.MsgUnchoke:
                p.Choked = false
                logger.Debug("Peer %s unchoked us", p.Addr)
                go t.requestPieces(p)

            case peer.MsgInterested:
                p.Interested = true
                logger.Debug("Peer %s is interested", p.Addr)

            case peer.MsgNotInterested:
                p.Interested = false
                logger.Debug("Peer %s is not interested", p.Addr)

            case peer.MsgHave:
                pieceIndex := binary.BigEndian.Uint32(payload)
                p.Bitfield.SetPiece(int(pieceIndex))
                logger.Debug("Peer %s has piece %d", p.Addr, pieceIndex)

            case peer.MsgBitfield:
                p.Bitfield = peer.NewBitfield(len(payload) * 8)
                copy(p.Bitfield, payload)
                logger.Debug("Received bitfield from peer %s", p.Addr)

            case peer.MsgRequest:
                if len(payload) < 12 {
                    logger.Error("Received invalid request message from peer %s", p.Addr)
                    continue
                }
                index := binary.BigEndian.Uint32(payload[0:4])
                begin := binary.BigEndian.Uint32(payload[4:8])
                length := binary.BigEndian.Uint32(payload[8:12])
                go t.handlePieceRequest(p, index, begin, length)

            case peer.MsgPiece:
                if len(payload) < 8 {
                    logger.Error("Received invalid piece message from peer %s", p.Addr)
                    continue
                }
                index := binary.BigEndian.Uint32(payload[0:4])
                // begin := binary.BigEndian.Uint32(payload[4:8])
                data := payload[8:]
                err := t.FileManager.WritePiece(int(index), data)
                if err != nil {
                    logger.Error("Failed to write piece %d: %v", index, err)
                } else {
                    logger.Debug("Received piece %d from peer %s", index, p.Addr)
                    t.updateDownloadedPieces(int(index))
                }

            case peer.MsgCancel:
                if len(payload) < 12 {
                    logger.Error("Received invalid cancel message from peer %s", p.Addr)
                    continue
                }
                index := binary.BigEndian.Uint32(payload[0:4])
                // begin := binary.BigEndian.Uint32(payload[4:8])
                // length := binary.BigEndian.Uint32(payload[8:12])
                logger.Debug("Received cancel request for piece %d from peer %s", index, p.Addr)
                // Implement cancellation logic here if needed

            case peer.MsgKeepAlive:
                logger.Debug("Received keep-alive from peer %s", p.Addr)

            default:
                logger.Warn("Received unknown message type %d from peer %s", msgType, p.Addr)
            }
        }
    }
}

func (t *Torrent) requestPieces(p *peer.Peer) {
    for i, piece := range t.Pieces {
        if !piece.Downloaded && p.Bitfield.HasPiece(i) {
            go t.requestPiece(p, piece)
        }
    }
}

func (t *Torrent) requestPiece(p *peer.Peer, piece Piece) {
    const blockSize = 16 * 1024 // 16 KB blocks
    numBlocks := (piece.Length + blockSize - 1) / blockSize

    for i := int64(0); i < numBlocks; i++ {
        begin := i * blockSize
        length := int64(blockSize)
        if begin+length > piece.Length {
            length = piece.Length - begin
        }

        msg := peer.NewRequestMessage(uint32(piece.Index), uint32(begin), uint32(length))
        err := p.SendMessage(peer.MsgRequest, msg)
        if err != nil {
            logger.Error("Failed to send request for piece %d to peer %s: %v", piece.Index, p.Addr, err)
            return
        }
    }
}

func (t *Torrent) handlePieceRequest(p *peer.Peer, index, begin, length uint32) {
    data, err := t.FileManager.ReadPiece(int(index), int64(begin), int64(length))
    if err != nil {
        logger.Error("Failed to read piece %d: %v", index, err)
        return
    }

    msg := peer.NewPieceMessage(index, begin, data)
    err = p.SendMessage(peer.MsgPiece, msg)
    if err != nil {
        logger.Error("Failed to send piece %d to peer %s: %v", index, p.Addr, err)
    }
}

func (t *Torrent) updateDownloadedPieces(index int) {
    t.Pieces[index].Downloaded = true
    t.Left -= t.Pieces[index].Length
    t.Downloaded += t.Pieces[index].Length

    // Check if the piece hash matches
    pieceData, err := t.FileManager.ReadPiece(index, 0, t.Pieces[index].Length)
    if err != nil {
        logger.Error("Failed to read piece %d for hash verification: %v", index, err)
        return
    }

    if !t.verifyPieceHash(index, pieceData) {
        logger.Warn("Piece %d failed hash verification", index)
        t.Pieces[index].Downloaded = false
        t.Left += t.Pieces[index].Length
        t.Downloaded -= t.Pieces[index].Length
        // Re-download this piece
        for _, p := range t.Peers {
            if p.Bitfield.HasPiece(index) {
                go t.requestPiece(p, t.Pieces[index])
                break
            }
        }
    } else {
        logger.Info("Piece %d downloaded and verified", index)
    }

    // Flush data to disk
    if err := t.FileManager.Flush(); err != nil {
        logger.Error("Failed to flush data to disk: %v", err)
    }
}

func (t *Torrent) verifyPieceHash(index int, data []byte) bool {
    hash := sha1.Sum(data)
    return bytes.Equal(hash[:], t.Pieces[index].Hash[:])
}

func (t *Torrent) requestMorePeers(ctx context.Context) {
	peers, err := t.Tracker.Announce(t.Downloaded, t.Uploaded, t.Left)
	if err != nil {
		logger.Error("Failed to request more peers: %v", err)
		return
	}

	for _, addr := range peers {
		go t.connectToPeers(ctx, addr)
	}
}

func generatePeerID() [20]byte {
	var id [20]byte
	rand.Read(id[:])
	return id
}

func calculatePieceLength(metaInfo *MetaInfo, index int) int64 {
    if metaInfo.Info.PieceLength == 0 {
        return 0 // Return 0 if PieceLength is not set
    }
    if index == metaInfo.NumPieces()-1 {
        return metaInfo.TotalLength() % metaInfo.Info.PieceLength
    }
    return metaInfo.Info.PieceLength
}

func GeneratePeerID() [20]byte {
	var id [20]byte
	rand.Read(id[:])
	return id
}

func (t *Torrent) connectToPeers(ctx context.Context, addr net.Addr) {
    if err := t.peerLimiter.Wait(ctx); err != nil {
        logger.Error("Rate limit error: %v", err)
        return
    }

    t.failedPeersMutex.Lock()
    if _, failed := t.failedPeers[addr.String()]; failed {
        t.failedPeersMutex.Unlock()
        logger.Info("Skipping recently failed peer: %s", addr)
        return
    }
    t.failedPeersMutex.Unlock()

    infoHash, err := t.MetaInfo.InfoHash()
    if err != nil {
        logger.Error("Failed to get info hash: %v", err)
        return
    }

    p := peer.NewPeer(addr, t.PeerID, infoHash)

    err = p.Connect(ctx)
    if err != nil {
        if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
            logger.Warn("Connection to peer %s timed out", addr)
        } else {
            logger.Error("Failed to connect to peer %s: %v", addr, err)
        }
        return
    }

    t.activePeersMutex.Lock()
    t.ActivePeers++
    t.activePeersMutex.Unlock()

    go t.handlePeer(ctx, p)
}

func (t *Torrent) SetFileManager(fm *file.FileManager) {
	t.FileManager = fm
}

// Update the UseDHT method
func (t *Torrent) UseDHT(ctx context.Context) error {
    logger.Info("Initializing DHT for peer discovery")

    config := dht.NewDefaultServerConfig()
    config.StartingNodes = func() ([]dht.Addr, error) {
        return dht.GlobalBootstrapAddrs("udp")
    }

    var err error
    t.dhtNode, err = dht.NewServer(config)
    if err != nil {
        return fmt.Errorf("failed to create DHT server: %v", err)
    }

    // Convert InfoHash to metainfo.Hash
    infoHash, err := t.MetaInfo.InfoHash()
    if err != nil {
        return fmt.Errorf("failed to get info hash: %v", err)
    }
    hash := metainfo.HashBytes(infoHash[:])

    // Start DHT announce and peer discovery
    go t.dhtAnnounceAndDiscover(ctx, hash)

    return nil
}

func (t *Torrent) dhtAnnounceAndDiscover(ctx context.Context, hash metainfo.Hash) {
    announceInterval := 5 * time.Minute
    discoverInterval := 30 * time.Second

    announceTicker := time.NewTicker(announceInterval)
    discoverTicker := time.NewTicker(discoverInterval)

    defer announceTicker.Stop()
    defer discoverTicker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-announceTicker.C:
            t.dhtNode.AddNodesFromFile("dht.dat")
            t.dhtNode.Announce(hash, 0, false)
        case <-discoverTicker.C:
            t.discoverPeers(ctx, hash)
        }
    }
}

func (t *Torrent) discoverPeers(ctx context.Context, hash metainfo.Hash) {
    logger.Debug("Discovering peers via DHT")
    
    var infoHash [20]byte
    copy(infoHash[:], hash[:])

    // Use AnnounceTraversal with proper error handling
    traversal, err := t.dhtNode.AnnounceTraversal(infoHash)
    if err != nil {
        logger.Error("Failed to start DHT traversal: %v", err)
        return
    }

    // Process the results
    for {
        select {
        case <-ctx.Done():
            return
        case r, ok := <-traversal.Peers:
            if !ok {
                // Channel closed, no more results
                return
            }
            // Assuming r has a method to get the address
            netAddr := &net.TCPAddr{
                IP:   r.Addr.IP,
                Port: r.Addr.Port,
            }
            newPeer := peer.NewPeer(netAddr, t.PeerID, infoHash)
            go t.connectAndHandlePeer(ctx, newPeer)
        }
    }
}

func (t *Torrent) connectAndHandlePeer(ctx context.Context, p *peer.Peer) {
    err := p.Connect(ctx)
    if err != nil {
        logger.Error("Failed to connect to peer %s: %v", p.Addr, err)
        return
    }

    t.activePeersMutex.Lock()
    t.ActivePeers++
    t.activePeersMutex.Unlock()

    defer func() {
        p.Disconnect()
        t.activePeersMutex.Lock()
        t.ActivePeers--
        t.activePeersMutex.Unlock()
    }()

    t.handlePeer(ctx, p)
}

