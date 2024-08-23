package peer

import (
	"bytes"
	"errors"
	"io"
	"net"
	"time"
)

const (
	ProtocolIdentifier = "BitTorrent protocol"
	HandshakeLength    = 68
	HandshakeTimeout   = 5 * time.Second
)

// Protocol defines the BitTorrent peer protocol
type Protocol struct {
	Conn     net.Conn
	InfoHash [20]byte
	PeerID   [20]byte
}

// NewProtocol creates a new Protocol instance
func NewProtocol(conn net.Conn, infoHash [20]byte, peerID [20]byte) *Protocol {
	return &Protocol{
		Conn:     conn,
		InfoHash: infoHash,
		PeerID:   peerID,
	}
}

// Handshake performs the BitTorrent handshake
func (p *Protocol) Handshake() error {
	// Set deadline for the handshake
	err := p.Conn.SetDeadline(time.Now().Add(HandshakeTimeout))
	if err != nil {
		return err
	}
	defer p.Conn.SetDeadline(time.Time{}) // Reset the deadline after handshake

	// Construct handshake message
	handshake := make([]byte, HandshakeLength)
	handshake[0] = byte(len(ProtocolIdentifier))
	copy(handshake[1:20], ProtocolIdentifier)
	// 8 reserved bytes
	copy(handshake[28:48], p.InfoHash[:])
	copy(handshake[48:], p.PeerID[:])

	// Send handshake
	_, err = p.Conn.Write(handshake)
	if err != nil {
		return err
	}

	// Receive and validate peer's handshake
	response := make([]byte, HandshakeLength)
	_, err = io.ReadFull(p.Conn, response)
	if err != nil {
		return err
	}

	if response[0] != byte(len(ProtocolIdentifier)) {
		return errors.New("invalid handshake length")
	}

	if !bytes.Equal(response[1:20], []byte(ProtocolIdentifier)) {
		return errors.New("invalid protocol identifier")
	}

	if !bytes.Equal(response[28:48], p.InfoHash[:]) {
		return errors.New("info hash mismatch")
	}

	// Peer ID is in the last 20 bytes, but we don't need to validate it

	return nil
}