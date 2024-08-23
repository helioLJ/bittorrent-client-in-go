package util

import (
	"bytes"
	"testing"
	"time"
)

func TestGeneratePeerID(t *testing.T) {
	id1 := GeneratePeerID()
	id2 := GeneratePeerID()

	if bytes.Equal(id1[:], id2[:]) {
		t.Error("Generated peer IDs are not unique")
	}
}

func TestHashInfoDict(t *testing.T) {
	info := []byte("test info dictionary")
	hash := HashInfoDict(info)

	if len(hash) != 20 {
		t.Errorf("Expected hash length of 20, got %d", len(hash))
	}
}

func TestRandomDuration(t *testing.T) {
	min := time.Second
	max := time.Minute

	for i := 0; i < 100; i++ {
		duration := RandomDuration(min, max)
		if duration < min || duration > max {
			t.Errorf("Generated duration %v is outside the range [%v, %v]", duration, min, max)
		}
	}
}

func TestMin(t *testing.T) {
	tests := []struct {
		a, b, expected int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{0, 0, 0},
		{-1, 1, -1},
	}

	for _, test := range tests {
		result := Min(test.a, test.b)
		if result != test.expected {
			t.Errorf("Min(%d, %d) = %d, expected %d", test.a, test.b, result, test.expected)
		}
	}
}

func TestMax(t *testing.T) {
	tests := []struct {
		a, b, expected int
	}{
		{1, 2, 2},
		{2, 1, 2},
		{0, 0, 0},
		{-1, 1, 1},
	}

	for _, test := range tests {
		result := Max(test.a, test.b)
		if result != test.expected {
			t.Errorf("Max(%d, %d) = %d, expected %d", test.a, test.b, result, test.expected)
		}
	}
}