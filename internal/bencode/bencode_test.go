package bencode

import (
	"bytes"
	"reflect"
	"testing"
)

func TestEncode(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected []byte
		wantErr  bool
	}{
		// Existing test cases...
		{"encode empty list", []interface{}{}, []byte("le"), false},
		{"encode nested list", []interface{}{1, []interface{}{2, 3}}, []byte("li1eli2ei3eee"), false},
		{"encode empty dict", map[string]interface{}{}, []byte("de"), false},
		{"encode nested dict", map[string]interface{}{"a": 1, "b": map[string]interface{}{"c": 2}}, []byte("d1:ai1e1:bd1:ci2eee"), false},
		{"encode nil", nil, []byte("e"), false},
		{"encode float", 3.14, nil, true}, // This should fail as bencode doesn't support floats
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Encode(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Encode() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("Encode() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDecode(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected interface{}
		wantErr  bool
	}{
		// Existing test cases...
		{"decode empty list", []byte("le"), []interface{}{}, false},
		{"decode nested list", []byte("li1eli2ei3eee"), []interface{}{int64(1), []interface{}{int64(2), int64(3)}}, false},
		{"decode empty dict", []byte("de"), map[string]interface{}{}, false},
		{"decode nested dict", []byte("d1:ai1e1:bd1:ci2eee"), map[string]interface{}{"a": int64(1), "b": map[string]interface{}{"c": int64(2)}}, false},
		{"decode invalid bencode", []byte("invalid"), nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decode(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Decode() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("Decode() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestEncodeErrors(t *testing.T) {
    tests := []struct {
        name    string
        input   interface{}
        wantErr bool
    }{
        {"encode complex number", complex(1, 2), true},
        {"encode function", func() {}, true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            _, err := Encode(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("Encode() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}

func TestDecodeErrors(t *testing.T) {
    tests := []struct {
        name    string
        input   []byte
        wantErr bool
    }{
        {"decode incomplete data", []byte("l"), true},
        {"decode invalid integer", []byte("i12a34e"), true},
        {"decode invalid string length", []byte("3:ab"), true},
        {"decode invalid dict key", []byte("di1e3:fooee"), true},
        {"decode extra data", []byte("i123eextra"), true},
        {"decode incomplete dict", []byte("d3:foo"), true},
        {"decode invalid string length", []byte("3x:abc"), true},
        {"decode negative string length", []byte("-1:"), true},
        {"decode very large string length", []byte("999999999999999999:abc"), true},
        {"decode string length larger than MaxInt32", []byte("2147483648:abc"), true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            _, err := Decode(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("Decode() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}

func TestDecodeValueErrors(t *testing.T) {
    tests := []struct {
        name    string
        input   []byte
        wantErr bool
    }{
        {"decode empty data", []byte{}, true},
        {"decode invalid type", []byte{'x'}, true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            r := bytes.NewReader(tt.input)
            _, err := decodeValue(r)
            if (err != nil) != tt.wantErr {
                t.Errorf("decodeValue() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}

func TestDecodeEdgeCases(t *testing.T) {
    tests := []struct {
        name     string
        input    []byte
        expected interface{}
        wantErr  bool
    }{
        {"decode empty list", []byte("le"), []interface{}{}, false},
        {"decode empty dict", []byte("de"), map[string]interface{}{}, false},
        {"decode zero-length string", []byte("0:"), "", false},
        {"decode negative integer", []byte("i-123e"), int64(-123), false},
        {"decode max int64", []byte("i9223372036854775807e"), int64(9223372036854775807), false},
        {"decode min int64", []byte("i-9223372036854775808e"), int64(-9223372036854775808), false},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := Decode(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("Decode() error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if !reflect.DeepEqual(got, tt.expected) {
                t.Errorf("Decode() = %v, want %v", got, tt.expected)
            }
        })
    }
}

func TestDecodeString(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {"valid string", "5:hello", "hello", false},
        {"empty string", "0:", "", false},
        {"invalid length", "5x:hello", "", true},
        {"negative length", "-1:hello", "", true},
        {"incomplete string", "5:hell", "", true},
        {"zero length string", "0:", "", false},
        {"very large string length", "999999999999999999:abc", "", true},
        {"string length larger than MaxInt32", "2147483648:abc", "", true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            r := bytes.NewReader([]byte(tt.input))
            got, err := decodeString(r)
            if (err != nil) != tt.wantErr {
                t.Errorf("decodeString() error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if got != tt.want {
                t.Errorf("decodeString() = %v, want %v", got, tt.want)
            }
        })
    }
}