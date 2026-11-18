// Package review implements the Review Helper standard (standards/): review
// files, the journal, the registry, building and iterating reviews, the fill
// sheet, exports, and the HTTP API, one file per area. Git access goes
// through internal/git.
package review

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"
)

// This implementation's distribution name and version, as `version` prints
// them, and the standard it implements.
const (
	Name            = "Review-Helper-Go"
	Version         = "0.001"
	StandardVersion = 1
	Generator       = Name + " " + Version
)

func now() int64 { return time.Now().Unix() }

// UUID7 makes a version 7 UUID.
func UUID7() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	ms := uint64(time.Now().UnixMilli())
	for i := range 6 {
		b[i] = byte(ms >> (40 - 8*i))
	}
	b[6] = 0x70 | b[6]&0x0f
	b[8] = 0x80 | b[8]&0x3f
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// MarshalJSON encodes compactly without HTML escaping. Map keys come out
// sorted, as in every file this backend writes.
func MarshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// MustJSON encodes values built in code, which always encode.
func MustJSON(v any) string {
	b, err := MarshalJSON(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// MarshalJSONIndent encodes for people to read.
func MarshalJSONIndent(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "   ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
