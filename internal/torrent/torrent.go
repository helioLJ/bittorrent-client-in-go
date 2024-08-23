package torrent

import (
	"bittorrent-client/internal/peer"
	"bittorrent-client/pkg/logger"
	"context"
	"fmt"
	"math/rand"
	"net"
	"time"
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
}

// Piece represents a piece of the torrent
type Piece struct {
	Index  int
	Hash   [20]byte
	Length int64
	Data   []byte
}

// NewTorrent creates a new Torrent instance
func NewTorrent(metaInfo *MetaInfo, tracker TrackerInterface) (*Torrent, error) {
	peerID := generatePeerID()
	pieces := make([]Piece, metaInfo.NumPieces())
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

	t := &Torrent{
        MetaInfo: metaInfo,
        PeerID:   peerID,
        Pieces:   pieces,
        Left:     metaInfo.TotalLength(),
        Tracker:  tracker,
    }

	return t, nil
}

// Download starts downloading the torrent
func (t *Torrent) Download(ctx context.Context) error {
	logger.Info("Starting torrent download")

	// Announce to the tracker and get peers
	peers, err := t.Tracker.Announce(t.Downloaded, t.Uploaded, t.Left)
	if err != nil {
		return fmt.Errorf("failed to announce to tracker: %w", err)
	}

	// Connect to peers
	for _, addr := range peers {
		go t.connectToPeer(ctx, addr)
	}

	// Main download loop
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Check if download is complete
			if t.isComplete() {
				logger.Info("Download complete")
				return nil
			}

			// Request more peers if needed
			if t.ActivePeers < 10 {
				go t.requestMorePeers(ctx)
			}

			time.Sleep(1 * time.Second)
		}
	}
}

func (t *Torrent) connectToPeer(ctx context.Context, addr net.Addr) {
	infoHash, err := t.MetaInfo.InfoHash()
	if err != nil {
		logger.Error("Failed to get info hash: %v", err)
		return
	}

	p := peer.NewPeer(addr, t.PeerID, infoHash)
	err = p.Connect(ctx)
	if err != nil {
		logger.Error("Failed to connect to peer %s: %v", addr, err)
		return
	}

	t.Peers = append(t.Peers, p)
	t.ActivePeers++

	go t.handlePeer(ctx, p)
}

func (t *Torrent) handlePeer(ctx context.Context, p *peer.Peer) {
	defer func() {
		p.Disconnect()
		t.ActivePeers--
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			msgType, _, err := p.ReadMessage()
			if err != nil {
				logger.Error("Error reading message from peer %s: %v", p.Addr, err)
				return
			}

			// Handle different message types
			switch msgType {
			case peer.MsgChoke:
				p.Choked = true
			case peer.MsgUnchoke:
				p.Choked = false
			case peer.MsgHave:
				// Update peer's bitfield
			case peer.MsgBitfield:
				// Set peer's bitfield
			case peer.MsgPiece:
				// Handle received piece
			}
		}
	}
}

func (t *Torrent) requestMorePeers(ctx context.Context) {
	peers, err := t.Tracker.Announce(t.Downloaded, t.Uploaded, t.Left)
	if err != nil {
		logger.Error("Failed to request more peers: %v", err)
		return
	}

	for _, addr := range peers {
		go t.connectToPeer(ctx, addr)
	}
}

func (t *Torrent) isComplete() bool {
	return t.Left == 0
}

func generatePeerID() [20]byte {
	var id [20]byte
	rand.Read(id[:])
	return id
}

func calculatePieceLength(metaInfo *MetaInfo, index int) int64 {
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