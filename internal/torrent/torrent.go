package torrent

import (
	"bittorrent-client/internal/common"
	"bittorrent-client/internal/file"
	"bittorrent-client/internal/peer"
	"bittorrent-client/internal/tracker"
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
	MetaInfo         *MetaInfo
	PeerID           [20]byte
	Peers            []*peer.Peer
	Pieces           []Piece
	Tracker          TrackerInterface
	Downloaded       int64
	Uploaded         int64
	Left             int64
	ActivePeers      int
	peerLimiter      *rate.Limiter
	activePeersMutex sync.Mutex
	failedPeers      map[string]time.Time
	failedPeersMutex sync.Mutex
	FileManager      *file.FileManager
	dhtNode          *dht.Server
	startTime        time.Time
	mu               sync.Mutex
	progressTicker   *time.Ticker
	progressDone     chan struct{}
	downloadMu       sync.Mutex
	PieceSize        int64
	activePeers      map[string]*peer.Peer
}

// Piece represents a piece of the torrent
type Piece struct {
	Index      int
	Hash       [20]byte
	Length     int64
	Data       []byte
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
func NewTorrent(metaInfo *MetaInfo, multiTracker *tracker.MultiTracker) (*Torrent, error) {
	peerID := generatePeerID()
	var pieces []Piece

	numPieces := metaInfo.NumPieces()
	if numPieces <= 0 {
		return nil, fmt.Errorf("invalid number of pieces: %d", numPieces)
	}

	pieces = make([]Piece, numPieces)
	for i := 0; i < numPieces; i++ {
		hash, err := metaInfo.PieceHash(i)
		if err != nil {
			return nil, fmt.Errorf("failed to get piece hash for index %d: %v", i, err)
		}
		pieces[i] = Piece{
			Index:  i,
			Hash:   hash,
			Length: calculatePieceLength(metaInfo, i),
		}
	}

	_, err := metaInfo.InfoHash()
	if err != nil {
		return nil, fmt.Errorf("failed to get info hash: %v", err)
	}

	t := &Torrent{
		MetaInfo:       metaInfo,
		PeerID:         peerID,
		Pieces:         pieces,
		Left:           metaInfo.TotalLength(),
		Tracker:        multiTracker,
		peerLimiter:    rate.NewLimiter(rate.Every(100*time.Millisecond), 1),
		failedPeers:    make(map[string]time.Time),
		FileManager:    nil,
		startTime:      time.Now(),
		progressTicker: time.NewTicker(5 * time.Second),
		progressDone:   make(chan struct{}),
		PieceSize:      metaInfo.Info.PieceLength,
		activePeers:    make(map[string]*peer.Peer),
	}

	go t.progressReporter()

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
	go t.announceWithRetry(ctx)

	// Get the info hash for DHT
	infoHash, err := t.MetaInfo.InfoHash()
	if err != nil {
		logger.Error("Failed to get info hash: %v", err)
		return err
	}

	// Convert InfoHash to metainfo.Hash
	hash := metainfo.HashBytes(infoHash[:])

	// Improve the DHT peer discovery
	if t.dhtNode != nil {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(30 * time.Second):
					t.discoverPeers(ctx, hash)
				}
			}
		}()
	}

	// Periodically request more peers
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				t.requestMorePeers(ctx)
			}
		}
	}()

	// Main download loop
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	defer func() {
		t.progressTicker.Stop()
		close(t.progressDone)
	}()

	// Add a timeout for the last piece
	lastPieceTimeout := time.After(10 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			logger.Info("Download cancelled")
			return ctx.Err()
		case <-lastPieceTimeout:
			logger.Warn("Timeout reached while trying to download the last piece")
			return fmt.Errorf("timeout reached while trying to download the last piece")
		case <-ticker.C:
			t.mu.Lock()
			activePeers := t.ActivePeers
			failedPeers := len(t.failedPeers)
			downloaded := t.Downloaded
			// left := t.Left
			t.mu.Unlock()

			logger.Info("Status: Active peers: %d, Failed peers: %d, Downloaded: %d/%d",
				activePeers, failedPeers, downloaded, t.MetaInfo.TotalLength())

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
	logger.Info("Verifying downloaded pieces...")

	for i, piece := range t.Pieces {
		if !piece.Downloaded {
			continue
		}

		err := t.verifyPiece(i)
		if err != nil {
			logger.Error("Piece %d failed verification: %v", i, err)
			piece.Downloaded = false
			t.Downloaded -= piece.Length
			t.Left += piece.Length
		} else {
			logger.Debug("Piece %d verified successfully", i)
		}
	}

	missingPieces := 0
	for _, piece := range t.Pieces {
		if !piece.Downloaded {
			missingPieces++
		}
	}

	if missingPieces > 0 {
		return fmt.Errorf("%d pieces failed verification or are missing", missingPieces)
	}

	logger.Info("All pieces verified successfully")
	return nil
}

func (t *Torrent) managePeerConnections(ctx context.Context) {
    ticker := time.NewTicker(30 * time.Second)
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

            // Clean up disconnected peers
            t.cleanupDisconnectedPeers()

            // Clean up failed peers
            t.cleanupFailedPeers()

            logger.Info("Peer connection status: Active peers: %d, Failed peers: %d", activePeers, len(t.failedPeers))
        }
    }
}

func (t *Torrent) cleanupDisconnectedPeers() {
	t.activePeersMutex.Lock()
	defer t.activePeersMutex.Unlock()

	for addr, peer := range t.activePeers {
		if time.Since(peer.GetLastActiveTime()) > 2*time.Minute {
			logger.Info("Peer %s inactive, disconnecting", addr)
			peer.Disconnect()
			delete(t.activePeers, addr)
			t.ActivePeers--
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
					logger.Debug("Error reading message from peer %s: %v", p.Addr, err)
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
				begin := binary.BigEndian.Uint32(payload[4:8])
				data := payload[8:]
				
				if int(index) >= len(t.Pieces) {
					logger.Error("Received piece with invalid index %d from peer %s (total pieces: %d)", index, p.Addr, len(t.Pieces))
					continue
				}
				
				if err := t.processPiece(int(index), int64(begin), data); err != nil {
					logger.Error("Failed to process piece %d: %v", index, err)
				} else {
					logger.Debug("Received piece %d from peer %s", index, p.Addr)
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
			go t.requestPiece(p, i)
		}
	}
}

func (t *Torrent) requestPiece(p *peer.Peer, index int) error {
    piece := &t.Pieces[index]
    if piece.Downloaded {
        return nil
    }

    // Request the piece from the peer
    for begin := int64(0); begin < piece.Length; begin += t.PieceSize {
        length := min(t.PieceSize, piece.Length-begin)
        request := peer.NewRequestMessage(uint32(index), uint32(begin), uint32(length))
        if err := p.SendMessage(peer.MsgRequest, request); err != nil {
            return fmt.Errorf("failed to send request for piece %d: %w", index, err)
        }
    }

    return nil
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

func (t *Torrent) processPiece(index int, begin int64, data []byte) error {
    t.mu.Lock()
    defer t.mu.Unlock()

    piece := &t.Pieces[index]
    
    logger.Debug("Processing piece %d, begin: %d, length: %d", index, begin, len(data))

    if piece.Downloaded {
        logger.Debug("Piece %d already downloaded, skipping", index)
        return nil
    }

    if err := t.FileManager.WritePiece(index, begin, data); err != nil {
        logger.Error("Failed to write piece %d: %v", index, err)
        return fmt.Errorf("failed to write piece %d: %v", index, err)
    }
    logger.Debug("Piece %d written successfully", index)

    t.downloadMu.Lock()
    t.Downloaded += int64(len(data))
    t.Left -= int64(len(data))
    t.downloadMu.Unlock()

    if begin+int64(len(data)) == piece.Length {
        if err := t.verifyPiece(index); err != nil {
            logger.Error("Piece %d failed verification: %v", index, err)
            t.downloadMu.Lock()
            t.Downloaded -= piece.Length
            t.Left += piece.Length
            t.downloadMu.Unlock()
            return fmt.Errorf("piece %d failed verification: %v", index, err)
        }
        
        piece.Downloaded = true
        logger.Debug("Piece %d verified and marked as downloaded", index)
    }
    
    return nil
}

func (t *Torrent) verifyPiece(index int) error {
    piece := &t.Pieces[index]
    data, err := t.FileManager.ReadPiece(index, 0, piece.Length)
    if err != nil {
        return fmt.Errorf("failed to read piece %d: %w", index, err)
    }

    hash := sha1.Sum(data)
    if !bytes.Equal(hash[:], piece.Hash[:]) {
        return fmt.Errorf("piece %d hash mismatch", index)
    }

    piece.Downloaded = true
    return nil
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
    maxRetries := 5
    retryDelay := time.Second

    logger.Debug("Attempting to connect to peer %s (max retries: %d)", addr, maxRetries)

    for i := 0; i < maxRetries; i++ {
        if err := t.peerLimiter.Wait(ctx); err != nil {
            logger.Error("Rate limit error when connecting to %s: %v", addr, err)
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
            logger.Error("Failed to get info hash for peer %s: %v", addr, err)
            return
        }

        p := peer.NewPeer(addr, t.PeerID, infoHash)

        logger.Debug("Attempting connection to peer %s (attempt %d/%d)", addr, i+1, maxRetries)
        err = p.Connect(ctx)
        if err != nil {
            if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
                logger.Warn("Connection to peer %s timed out (attempt %d/%d)", addr, i+1, maxRetries)
            } else {
                logger.Warn("Failed to connect to peer %s: %v (attempt %d/%d)", addr, err, i+1, maxRetries)
            }
            if i < maxRetries-1 {
                logger.Debug("Retrying connection to peer %s in %v", addr, retryDelay)
                select {
                case <-time.After(retryDelay):
                    retryDelay *= 2
                case <-ctx.Done():
                    logger.Warn("Context cancelled while waiting to retry connection to peer %s", addr)
                    return
                }
            }
            continue
        }

        logger.Info("Successfully connected to peer %s", addr)

        t.activePeersMutex.Lock()
        t.ActivePeers++
        t.activePeersMutex.Unlock()

        go t.handlePeer(ctx, p)
        return
    }

    logger.Warn("Failed to connect to peer %s after %d attempts", addr, maxRetries)
    t.failedPeersMutex.Lock()
    t.failedPeers[addr.String()] = time.Now()
    t.failedPeersMutex.Unlock()
    logger.Debug("Marked peer %s as failed", addr)
}

// Update the SetFileManager function in the Torrent struct
func (t *Torrent) SetFileManager(fm *file.FileManager) error {
	t.FileManager = fm

	// Initialize the FileManager with the correct piece information
	files := make([]common.TorrentFile, len(t.MetaInfo.Info.Files))
	for i, f := range t.MetaInfo.Info.Files {
		files[i] = common.TorrentFile{
			Path:   strings.Join(f.Path, "/"),
			Length: f.Length,
		}
	}

	if len(files) == 0 {
		// Single file torrent
		files = append(files, common.TorrentFile{
			Path:   t.MetaInfo.Info.Name,
			Length: t.MetaInfo.Info.Length,
		})
	}

	err := fm.Initialize(files)
	if err != nil {
		return fmt.Errorf("failed to initialize FileManager: %v", err)
	}

	fm.SetPieceSize(t.MetaInfo.Info.PieceLength)
	fm.SetTotalLength(t.MetaInfo.TotalLength())

	return nil
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

    traversal, err := t.dhtNode.AnnounceTraversal(infoHash)
    if err != nil {
        logger.Error("Failed to start DHT traversal: %v", err)
        return
    }

    peerCount := 0
    uniquePeers := make(map[string]struct{})

    for {
        select {
        case <-ctx.Done():
            logger.Info("DHT peer discovery stopped, found %d unique peers", len(uniquePeers))
            return
        case r, ok := <-traversal.Peers:
            if !ok {
                logger.Info("DHT peer discovery finished, found %d unique peers", len(uniquePeers))
                return
            }
            addr := fmt.Sprintf("%s:%d", r.Addr.IP.String(), r.Addr.Port)
            if _, exists := uniquePeers[addr]; !exists {
                uniquePeers[addr] = struct{}{}
                peerCount++
                netAddr := &net.TCPAddr{
                    IP:   r.Addr.IP,
                    Port: r.Addr.Port,
                }
                go t.connectAndHandlePeer(ctx, peer.NewPeer(netAddr, t.PeerID, infoHash))
            }
        }
    }
}

func (t *Torrent) connectAndHandlePeer(ctx context.Context, p *peer.Peer) {
    if t.isFailed(p.Addr.String()) {
        return
    }

    logger.Debug("Attempting to connect to peer %s", p.Addr)

    ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()

    err := p.Connect(ctx)
    if err != nil {
        logger.Debug("Failed to connect to peer %s: %v", p.Addr, err)
        t.markFailed(p.Addr.String())
        return
    }

    t.activePeersMutex.Lock()
    t.ActivePeers++
    t.activePeers[p.Addr.String()] = p
    t.activePeersMutex.Unlock()

    logger.Info("Successfully connected to peer %s", p.Addr)

    defer func() {
        t.activePeersMutex.Lock()
        delete(t.activePeers, p.Addr.String())
        t.ActivePeers--
        t.activePeersMutex.Unlock()
        p.Disconnect()
    }()

    t.handlePeer(ctx, p)
}

func (t *Torrent) announceWithRetry(ctx context.Context) {
    maxRetries := 5
    baseDelay := time.Second

    for i := 0; i < maxRetries; i++ {
        peers, err := t.Tracker.Announce(t.Downloaded, t.Uploaded, t.Left)
        if err == nil {
            logger.Info("Received %d peers from trackers", len(peers))
            for _, addr := range peers {
                go t.connectToPeers(ctx, addr)
            }
            return
        }

        logger.Warn("Tracker announce failed (attempt %d/%d): %v", i+1, maxRetries, err)
        
        // Exponential backoff
        delay := time.Duration(1<<uint(i)) * baseDelay
        select {
        case <-ctx.Done():
            return
        case <-time.After(delay):
        }
    }

    logger.Error("All tracker announce attempts failed")
}

func (t *Torrent) progressReporter() {
	for {
		select {
		case <-t.progressTicker.C:
			t.reportProgress()
		case <-t.progressDone:
			return
		}
	}
}

func (t *Torrent) reportProgress() {
    t.mu.Lock()
    defer t.mu.Unlock()

    t.downloadMu.Lock()
    downloaded := t.Downloaded
    // left := t.Left
    t.downloadMu.Unlock()

    totalPieces := len(t.Pieces)
    downloadedPieces := 0
    for _, piece := range t.Pieces {
        if piece.Downloaded {
            downloadedPieces++
        }
    }
    progress := float64(downloadedPieces) / float64(totalPieces) * 100
    logger.Info("Download progress: %.2f%% (%d/%d pieces, %d/%d bytes)", 
        progress, downloadedPieces, totalPieces, downloaded, t.MetaInfo.TotalLength())
}


// Add these methods to your Torrent struct

// isFailed checks if a peer has been marked as failed
func (t *Torrent) isFailed(addr string) bool {
    t.failedPeersMutex.Lock()
    defer t.failedPeersMutex.Unlock()
    
    failTime, exists := t.failedPeers[addr]
    if !exists {
        return false
    }
    
    // Consider a peer failed for 5 minutes
    return time.Since(failTime) < 5*time.Minute
}

// markFailed marks a peer as failed
func (t *Torrent) markFailed(addr string) {
    t.failedPeersMutex.Lock()
    defer t.failedPeersMutex.Unlock()
    
    t.failedPeers[addr] = time.Now()
}

// You might also want to add a method to clean up old failed peers
func (t *Torrent) cleanupFailedPeers() {
    t.failedPeersMutex.Lock()
    defer t.failedPeersMutex.Unlock()
    
    for addr, failTime := range t.failedPeers {
        if time.Since(failTime) > 5*time.Minute {
            delete(t.failedPeers, addr)
        }
    }
}