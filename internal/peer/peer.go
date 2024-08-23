package peer

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
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
)

// Peer represents a peer in the BitTorrent network
type Peer struct {
	Addr      net.Addr
	Conn      net.Conn
	Choked    bool
	Interested bool
	PeerID    [20]byte
	InfoHash  [20]byte
}

// NewPeer creates a new Peer instance
func NewPeer(addr net.Addr, peerID [20]byte, infoHash [20]byte) *Peer {
	return &Peer{
		Addr:     addr,
		PeerID:   peerID,
		InfoHash: infoHash,
		Choked:   true,
	}
}

// Connect establishes a connection to the peer
func (p *Peer) Connect(ctx context.Context) error {
    dialer := net.Dialer{Timeout: 10 * time.Second}
    var err error
    p.Conn, err = dialer.DialContext(ctx, "tcp", p.Addr.String())
    if err != nil {
        return err
    }

    errChan := make(chan error, 1)
    go func() {
        errChan <- p.sendHandshake()
    }()

    select {
    case err := <-errChan:
        if err != nil {
            p.Conn.Close()
            return err
        }
    case <-time.After(10 * time.Second):
        p.Conn.Close()
        return errors.New("handshake timeout")
    }

    return p.readHandshake()
}

// Disconnect closes the connection to the peer
func (p *Peer) Disconnect() error {
	if p.Conn != nil {
		return p.Conn.Close()
	}
	return nil
}

func (p *Peer) sendHandshake() error {
	handshake := make([]byte, 68)
	handshake[0] = 19
	copy(handshake[1:20], []byte("BitTorrent protocol"))
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

	copy(p.InfoHash[:], handshake[28:48])
	copy(p.PeerID[:], handshake[48:])

	return nil
}

// ReadMessage reads a message from the peer
func (p *Peer) ReadMessage() (int, []byte, error) {
    p.Conn.SetReadDeadline(time.Now().Add(10 * time.Second))
    defer p.Conn.SetReadDeadline(time.Time{})

    lengthBuf := make([]byte, 4)
    _, err := io.ReadFull(p.Conn, lengthBuf)
    if err != nil {
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

    return int(messageBuf[0]), messageBuf[1:], nil
}


// SendMessage sends a message to the peer
func (p *Peer) SendMessage(messageType int, payload []byte) error {
	length := uint32(len(payload) + 1)
	buf := make([]byte, 4+length)
	binary.BigEndian.PutUint32(buf[0:4], length)
	buf[4] = byte(messageType)
	copy(buf[5:], payload)

	_, err := p.Conn.Write(buf)
	return err
}