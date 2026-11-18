// Package review implements the standard (standards/): review files, the
// journal, the registry, building and iterating reviews, the fill sheet,
// exports, and the HTTP API, one file per area. Git access goes through
// internal/git.
package review

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Name and Version identify this implementation: its distribution name and
// version, as `version` prints them.
const (
	Name            = "Review-Helper-Go"
	Version         = "0.001"
	StandardVersion = 1
)

// Generator is the meta and export generator string.
func Generator() string { return Name + " " + Version }

// Now is the current time in epoch seconds.
func Now() int64 { return time.Now().Unix() }

// UUID7 makes a version 7 UUID.
func UUID7() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	ms := uint64(time.Now().UnixMilli())
	for i := 0; i < 6; i++ {
		b[i] = byte(ms >> (40 - 8*i))
	}
	b[6] = 0x70 | (b[6] & 0x0f)
	b[8] = 0x80 | (b[8] & 0x3f)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// JSONEncode encodes compactly with map keys sorted (canonical, as the Perl
// backend writes) and without HTML escaping.
func JSONEncode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// MustJSON encodes v canonically and panics on failure: for values built in
// code, which always encode.
func MustJSON(v any) string {
	b, err := JSONEncode(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// JSONPretty encodes with sorted keys, indented by three spaces as the Perl
// backend's pretty output.
func JSONPretty(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "   ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// JSONDecode decodes into generic values, numbers as json.Number.
func JSONDecode(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("unexpected data after JSON value")
	}
	return v, nil
}

// errorf makes an error with a message for a person.
func errorf(format string, args ...any) error { return fmt.Errorf(format, args...) }

// joinLines joins problems one per line.
func joinLines(lines []string) string { return strings.Join(lines, "\n") }
