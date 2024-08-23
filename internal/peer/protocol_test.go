package peer

import (
	"bytes"
	"net"
	"testing"
	"time"
)

type mockConn struct {
	net.Conn
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
}

func (m *mockConn) Read(b []byte) (n int, err error) {
	return m.readBuf.Read(b)
}

func (m *mockConn) Write(b []byte) (n int, err error) {
	return m.writeBuf.Write(b)
}

func (m *mockConn) SetDeadline(t time.Time) error {
	return nil
}

func TestHandshake(t *testing.T) {
	infoHash := [20]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	peerID := [20]byte{20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}

	testCases := []struct {
		name          string
		mockHandshake []byte
		expectError   bool
	}{
		{
			name:          "Valid handshake",
			mockHandshake: createMockHandshake(infoHash, [20]byte{255, 254, 253, 252, 251}),
			expectError:   false,
		},
		{
			name:          "Invalid protocol identifier",
			mockHandshake: createInvalidProtocolHandshake(infoHash, [20]byte{255, 254, 253, 252, 251}),
			expectError:   true,
		},
		{
			name:          "Info hash mismatch",
			mockHandshake: createMockHandshake([20]byte{255, 254, 253, 252, 251}, [20]byte{255, 254, 253, 252, 251}),
			expectError:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockConn := &mockConn{
				readBuf:  bytes.NewBuffer(tc.mockHandshake),
				writeBuf: &bytes.Buffer{},
			}

			protocol := NewProtocol(mockConn, infoHash, peerID)

			err := protocol.Handshake()

			if tc.expectError && err == nil {
				t.Error("Expected an error, but got nil")
			}

			if !tc.expectError && err != nil {
				t.Errorf("Expected no error, but got: %v", err)
			}

			// Verify the sent handshake
			sentHandshake := mockConn.writeBuf.Bytes()
			if len(sentHandshake) != HandshakeLength {
				t.Errorf("Sent handshake length is incorrect. Expected %d, got %d", HandshakeLength, len(sentHandshake))
			}

			if sentHandshake[0] != byte(len(ProtocolIdentifier)) {
				t.Errorf("Incorrect protocol identifier length")
			}

			if !bytes.Equal(sentHandshake[1:20], []byte(ProtocolIdentifier)) {
				t.Errorf("Incorrect protocol identifier")
			}

			if !bytes.Equal(sentHandshake[28:48], infoHash[:]) {
				t.Errorf("Incorrect info hash in sent handshake")
			}

			if !bytes.Equal(sentHandshake[48:], peerID[:]) {
				t.Errorf("Incorrect peer ID in sent handshake")
			}
		})
	}
}

func createMockHandshake(infoHash [20]byte, peerID [20]byte) []byte {
	handshake := make([]byte, HandshakeLength)
	handshake[0] = byte(len(ProtocolIdentifier))
	copy(handshake[1:20], ProtocolIdentifier)
	// 8 reserved bytes
	copy(handshake[28:48], infoHash[:])
	copy(handshake[48:], peerID[:])
	return handshake
}

func createInvalidProtocolHandshake(infoHash [20]byte, peerID [20]byte) []byte {
	handshake := make([]byte, HandshakeLength)
	handshake[0] = byte(len(ProtocolIdentifier))
	copy(handshake[1:20], "Invalid protocol")
	// 8 reserved bytes
	copy(handshake[28:48], infoHash[:])
	copy(handshake[48:], peerID[:])
	return handshake
}

func TestHandshakeTimeout(t *testing.T) {
	infoHash := [20]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	peerID := [20]byte{20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}

	slowConn := &mockConn{
		readBuf:  bytes.NewBuffer(createMockHandshake(infoHash, peerID)),
		writeBuf: &bytes.Buffer{},
	}

	timeoutConn := &timeoutConn{
		Conn:    slowConn,
		timeout: HandshakeTimeout + 10*time.Millisecond,
	}

	protocol := NewProtocol(timeoutConn, infoHash, peerID)

	err := protocol.Handshake()

	if err == nil {
		t.Error("Expected a timeout error, but got nil")
	}
}

type timeoutConn struct {
	net.Conn
	timeout time.Duration
}

func (c *timeoutConn) Read(b []byte) (n int, err error) {
	time.Sleep(c.timeout)
	return 0, &net.OpError{Op: "read", Err: &timeoutError{}}
}

func (c *timeoutConn) Write(b []byte) (n int, err error) {
	return len(b), nil
}

func (c *timeoutConn) SetDeadline(t time.Time) error {
	return nil
}

type timeoutError struct{}

func (e *timeoutError) Error() string   { return "timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }