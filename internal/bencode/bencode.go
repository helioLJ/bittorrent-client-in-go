package bencode

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
)

// Encode encodes data into bencode format
func Encode(data interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if data == nil {
		return []byte("e"), nil
	}
	err := encodeValue(&buf, reflect.ValueOf(data))
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeValue(w io.Writer, v reflect.Value) error {
	switch v.Kind() {
	case reflect.Interface:
		return encodeValue(w, v.Elem())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		_, err := fmt.Fprintf(w, "i%de", v.Int())
		return err
	case reflect.String:
		_, err := fmt.Fprintf(w, "%d:%s", len(v.String()), v.String())
		return err
	case reflect.Slice, reflect.Array:
		_, err := w.Write([]byte("l"))
		if err != nil {
			return err
		}
		for i := 0; i < v.Len(); i++ {
			err = encodeValue(w, v.Index(i))
			if err != nil {
				return err
			}
		}
		_, err = w.Write([]byte("e"))
		return err
	case reflect.Map:
		_, err := w.Write([]byte("d"))
		if err != nil {
			return err
		}
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool {
			return keys[i].String() < keys[j].String()
		})
		for _, k := range keys {
			err = encodeValue(w, k)
			if err != nil {
				return err
			}
			err = encodeValue(w, v.MapIndex(k))
			if err != nil {
				return err
			}
		}
		_, err = w.Write([]byte("e"))
		return err
	default:
		return fmt.Errorf("unsupported type: %v", v.Kind())
	}
}

// Decode decodes bencoded data
func Decode(data []byte) (interface{}, error) {
	r := bytes.NewReader(data)
	result, err := decodeValue(r)
	if err != nil {
		return nil, err
	}
	// Check if we've consumed all the input
	if r.Len() > 0 {
		return nil, fmt.Errorf("unexpected data after valid bencode")
	}
	return result, nil
}

func decodeValue(r *bytes.Reader) (interface{}, error) {
	b, err := r.ReadByte()
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("unexpected end of data")
		}
		return nil, err
	}

	switch b {
	case 'i':
		return decodeInt(r)
	case 'l':
		return decodeList(r)
	case 'd':
		return decodeDict(r)
	default:
		r.UnreadByte()
		return decodeString(r)
	}
}

func decodeInt(r *bytes.Reader) (int64, error) {
	var num []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		if b == 'e' {
			break
		}
		num = append(num, b)
	}
	return strconv.ParseInt(string(num), 10, 64)
}

func decodeString(r *bytes.Reader) (string, error) {
    var length []byte
    for {
        b, err := r.ReadByte()
        if err != nil {
            return "", err
        }
        if b == ':' {
            break
        }
        length = append(length, b)
    }
    l, err := strconv.ParseInt(string(length), 10, 64)
    if err != nil {
        return "", fmt.Errorf("invalid string length: %s", string(length))
    }
    if l < 0 {
        return "", fmt.Errorf("negative string length: %d", l)
    }
    if l > math.MaxInt32 {
        return "", fmt.Errorf("string length too large: %d", l)
    }
    str := make([]byte, l)
    n, err := io.ReadFull(r, str)
    if err != nil {
        return "", err
    }
    if int64(n) != l {
        return "", fmt.Errorf("unexpected end of string data")
    }
    return string(str), nil
}


func decodeList(r *bytes.Reader) ([]interface{}, error) {
	list := make([]interface{}, 0)  // Initialize with an empty slice
	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if b == 'e' {
			break
		}
		r.UnreadByte()
		item, err := decodeValue(r)
		if err != nil {
			return nil, err
		}
		list = append(list, item)
	}
	return list, nil
}

func decodeDict(r *bytes.Reader) (map[string]interface{}, error) {
    dict := make(map[string]interface{})
    for {
        b, err := r.ReadByte()
        if err != nil {
            return nil, err
        }
        if b == 'e' {
            break
        }
        r.UnreadByte()
        key, err := decodeValue(r)
        if err != nil {
            return nil, err
        }
        // Check if the key is a string
        keyStr, ok := key.(string)
        if !ok {
            return nil, fmt.Errorf("invalid dictionary key type: %T", key)
        }
        value, err := decodeValue(r)
        if err != nil {
            return nil, err
        }
        dict[keyStr] = value
    }
    return dict, nil
}