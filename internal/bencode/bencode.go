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

// Unmarshal decodes bencoded data into a Go structure
func Unmarshal(data []byte, v interface{}) error {
	r := bytes.NewReader(data)
	value, err := decodeValue(r)
	if err != nil {
		return err
	}
	return populateValue(reflect.ValueOf(v).Elem(), value)
}

func populateValue(v reflect.Value, data interface{}) error {
	switch v.Kind() {
	case reflect.Interface:
		v.Set(reflect.ValueOf(data))
	case reflect.Ptr:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		return populateValue(v.Elem(), data)
	case reflect.Struct:
		m, ok := data.(map[string]interface{})
		if !ok {
			return fmt.Errorf("expected map for struct, got %T", data)
		}
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			fieldValue := v.Field(i)
			fieldName := field.Tag.Get("bencode")
			if fieldName == "" {
				fieldName = field.Name
			}
			if value, ok := m[fieldName]; ok {
				if err := populateValue(fieldValue, value); err != nil {
					return err
				}
			}
		}
	case reflect.Slice:
		s, ok := data.([]interface{})
		if !ok {
			return fmt.Errorf("expected slice, got %T", data)
		}
		slice := reflect.MakeSlice(v.Type(), len(s), len(s))
		for i := 0; i < len(s); i++ {
			if err := populateValue(slice.Index(i), s[i]); err != nil {
				return err
			}
		}
		v.Set(slice)
	case reflect.Map:
		m, ok := data.(map[string]interface{})
		if !ok {
			return fmt.Errorf("expected map, got %T", data)
		}
		mapType := reflect.MapOf(v.Type().Key(), v.Type().Elem())
		mapValue := reflect.MakeMap(mapType)
		for key, value := range m {
			mapKey := reflect.ValueOf(key)
			mapElem := reflect.New(v.Type().Elem()).Elem()
			if err := populateValue(mapElem, value); err != nil {
				return err
			}
			mapValue.SetMapIndex(mapKey, mapElem)
		}
		v.Set(mapValue)
	case reflect.String:
		str, ok := data.(string)
		if !ok {
			return fmt.Errorf("expected string, got %T", data)
		}
		v.SetString(str)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		num, ok := data.(int64)
		if !ok {
			return fmt.Errorf("expected int, got %T", data)
		}
		v.SetInt(num)
	default:
		return fmt.Errorf("unsupported kind: %s", v.Kind())
	}
	return nil
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