package peer

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestPeer(t *testing.T) {
	// Start a mock peer server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start mock server: %v", err)
	}
	defer listener.Close()

	serverChan := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverChan <- fmt.Errorf("accept error: %v", err)
			return
		}
		defer conn.Close()

		// Send a mock handshake
		handshake := make([]byte, 68)
		handshake[0] = 19
		copy(handshake[1:20], []byte("BitTorrent protocol"))
		_, err = conn.Write(handshake)
		if err != nil {
			serverChan <- fmt.Errorf("handshake write error: %v", err)
			return
		}

		// Read client's handshake
		clientHandshake := make([]byte, 68)
		_, err = io.ReadFull(conn, clientHandshake)
		if err != nil {
			serverChan <- fmt.Errorf("client handshake read error: %v", err)
			return
		}

		// Echo any received messages
		for {
			buf := make([]byte, 1024)
			n, err := conn.Read(buf)
			if err != nil {
				if err == io.EOF {
					serverChan <- nil // Connection closed normally
				} else {
					serverChan <- fmt.Errorf("read error: %v", err)
				}
				return
			}
			_, err = conn.Write(buf[:n])
			if err != nil {
				serverChan <- fmt.Errorf("write error: %v", err)
				return
			}
		}
	}()

	// Create a peer
	addr := listener.Addr()
	peerID := [20]byte{1, 2, 3, 4, 5}
	infoHash := [20]byte{6, 7, 8, 9, 10}
	peer := NewPeer(addr, peerID, infoHash)

	// Test Connect
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = peer.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Test SendMessage
	err = peer.SendMessage(MsgInterested, nil)
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	// Test ReadMessage
	msgChan := make(chan struct {
		msgType int
		payload []byte
		err     error
	})

	go func() {
		msgType, payload, err := peer.ReadMessage()
		msgChan <- struct {
			msgType int
			payload []byte
			err     error
		}{msgType, payload, err}
	}()

	select {
	case msg := <-msgChan:
		if msg.err != nil {
			t.Fatalf("ReadMessage failed: %v", msg.err)
		}
		if msg.msgType != MsgInterested {
			t.Errorf("Expected message type %d, got %d", MsgInterested, msg.msgType)
		}
		if len(msg.payload) != 0 {
			t.Errorf("Expected empty payload, got %v", msg.payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadMessage timed out")
	}

	// Test Disconnect
	err = peer.Disconnect()
	if err != nil {
		t.Fatalf("Disconnect failed: %v", err)
	}

	// Check if the server encountered any errors
	select {
	case err := <-serverChan:
		if err != nil {
			t.Fatalf("Server encountered an error: %v", err)
		}
	case <-time.After(1 * time.Second):
		// If we don't receive an error within 1 second, assume the server is fine
	}
}