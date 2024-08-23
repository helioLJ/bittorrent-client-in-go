package peer

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const (
	MsgChoke = iota
	MsgUnchoke
	MsgInterested
	MsgNotInterested
	MsgHave
	MsgBitfield
	MsgRequest
	MsgPiece
	MsgCancel
	MsgKeepAlive = -1
)

// Peer represents a peer in the BitTorrent network
type Peer struct {
	Addr           net.Addr
	Conn           net.Conn
	Choked         bool
	Interested     bool
	PeerID         [20]byte
	InfoHash       [20]byte
	LastActiveTime time.Time
	stopKeepalive  chan struct{}
	mu             sync.Mutex
	Bitfield       Bitfield
	keepaliveOnce  sync.Once
}

// Bitfield represents the pieces that a peer has
type Bitfield []byte

// NewBitfield creates a new Bitfield with the given size
func NewBitfield(size int) Bitfield {
	return make(Bitfield, (size+7)/8)
}

// HasPiece returns true if the peer has the piece at the given index
func (bf Bitfield) HasPiece(index int) bool {
	byteIndex := index / 8
	offset := index % 8
	if byteIndex < 0 || byteIndex >= len(bf) {
		return false
	}
	return bf[byteIndex]>>(7-offset)&1 != 0
}

// SetPiece sets the bit for the piece at the given index
func (bf Bitfield) SetPiece(index int) {
	byteIndex := index / 8
	offset := index % 8
	if byteIndex < 0 || byteIndex >= len(bf) {
		return
	}
	bf[byteIndex] |= 1 << (7 - offset)
}

// NewPeer creates a new Peer instance
func NewPeer(addr net.Addr, peerID [20]byte, infoHash [20]byte) *Peer {
	return &Peer{
		Addr:           addr,
		PeerID:         peerID,
		InfoHash:       infoHash,
		Choked:         true,
		LastActiveTime: time.Now(),
		stopKeepalive:  make(chan struct{}),
	}
}

// Connect establishes a connection to the peer
func (p *Peer) Connect(ctx context.Context) error {
	dialer := &net.Dialer{
		Timeout: 30 * time.Second, // Increase timeout
	}
	conn, err := dialer.DialContext(ctx, "tcp", p.Addr.String())
	if err != nil {
		return err
	}
	p.Conn = conn
	return p.sendHandshake()
}

// Disconnect closes the connection to the peer
func (p *Peer) Disconnect() error {
	p.StopKeepalive()
	if p.Conn != nil {
		return p.Conn.Close()
	}
	return nil
}

func (p *Peer) sendHandshake() error {
	handshake := make([]byte, 68)
	handshake[0] = 19
	copy(handshake[1:20], []byte("BitTorrent protocol"))
	// Set some reserved bytes for extensions
	handshake[20] = 0x00
	handshake[21] = 0x00
	handshake[22] = 0x00
	handshake[23] = 0x01 // Indicate we support extensions
	copy(handshake[28:48], p.InfoHash[:])
	copy(handshake[48:], p.PeerID[:])

	_, err := p.Conn.Write(handshake)
	return err
}

func (p *Peer) readHandshake() error {
	handshake := make([]byte, 68)
	_, err := io.ReadFull(p.Conn, handshake)
	if err != nil {
		return err
	}

	if handshake[0] != 19 {
		return errors.New("invalid handshake")
	}

	if string(handshake[1:20]) != "BitTorrent protocol" {
		return errors.New("invalid protocol")
	}

	if !bytes.Equal(handshake[28:48], p.InfoHash[:]) {
		return errors.New("info hash mismatch")
	}

	return nil
}

// ReadMessage reads a message from the peer
func (p *Peer) ReadMessage() (int, []byte, error) {
	err := p.Conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	if err != nil {
		return 0, nil, fmt.Errorf("failed to set read deadline: %w", err)
	}
	defer p.Conn.SetReadDeadline(time.Time{})

	lengthBuf := make([]byte, 4)
	_, err = io.ReadFull(p.Conn, lengthBuf)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return 0, nil, fmt.Errorf("read timeout")
		}
		return 0, nil, err
	}

	length := binary.BigEndian.Uint32(lengthBuf)
	if length == 0 {
		return 0, nil, nil // keep-alive message
	}

	messageBuf := make([]byte, length)
	_, err = io.ReadFull(p.Conn, messageBuf)
	if err != nil {
		return 0, nil, err
	}

	p.LastActiveTime = time.Now()
	return int(messageBuf[0]), messageBuf[1:], nil
}

// SendMessage sends a message to the peer
func (p *Peer) SendMessage(messageType int, payload []byte) error {
	if messageType == MsgKeepAlive {
		_, err := p.Conn.Write([]byte{0, 0, 0, 0})
		return err
	}

	length := uint32(len(payload) + 1)
	buf := make([]byte, 4+length)
	binary.BigEndian.PutUint32(buf[0:4], length)
	buf[4] = byte(messageType)
	copy(buf[5:], payload)

	err := p.Conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if err != nil {
		return fmt.Errorf("failed to set write deadline: %w", err)
	}
	defer p.Conn.SetWriteDeadline(time.Time{})

	_, err = p.Conn.Write(buf)
	return err
}

func (p *Peer) keepAlive(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopKeepalive:
			return
		case <-ticker.C:
			err := p.SendMessage(MsgKeepAlive, nil)
			if err != nil {
				log.Printf("Failed to send keep-alive to peer %s: %v", p.Addr, err)
				return
			}
		}
	}
}

func (p *Peer) StopKeepalive() {
	p.keepaliveOnce.Do(func() {
		close(p.stopKeepalive)
	})
}

func (p *Peer) UpdateLastActiveTime() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.LastActiveTime = time.Now()
}

func (p *Peer) GetLastActiveTime() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.LastActiveTime
}

func NewRequestMessage(index, begin, length uint32) []byte {
	msg := make([]byte, 12)
	binary.BigEndian.PutUint32(msg[0:4], index)
	binary.BigEndian.PutUint32(msg[4:8], begin)
	binary.BigEndian.PutUint32(msg[8:12], length)
	return msg
}

func NewPieceMessage(index, begin uint32, data []byte) []byte {
	msg := make([]byte, 8+len(data))
	binary.BigEndian.PutUint32(msg[0:4], index)
	binary.BigEndian.PutUint32(msg[4:8], begin)
	copy(msg[8:], data)
	return msg
}