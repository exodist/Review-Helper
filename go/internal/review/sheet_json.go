package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

// Reading the JSON an agent returns in a sheet, value by value, and the
// canonical form revs and stored guide steps use.

// canonicalJSON encodes v compactly with object keys sorted, numbers as
// written, and U+2028 and U+2029 raw, as the Perl backend writes them (Go
// escapes them even with HTML escaping off).
func canonicalJSON(v any) ([]byte, error) {
	b, err := MarshalJSON(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var generic any
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	if b, err = MarshalJSON(generic); err != nil {
		return nil, err
	}
	return rawLineSeparators(b), nil
}

// rawLineSeparators writes the \u2028 and \u2029 escapes of encoded JSON
// as the characters. An escape is one only where the backslash is not
// itself escaped.
func rawLineSeparators(b []byte) []byte {
	if !bytes.Contains(b, []byte(`\u202`)) {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' {
			out = append(out, b[i])
			continue
		}
		// Valid JSON has a character after every backslash.
		switch string(b[i:min(i+6, len(b))]) {
		case `\u2028`:
			out = append(out, "\u2028"...)
			i += 5
		case `\u2029`:
			out = append(out, "\u2029"...)
			i += 5
		default: // any other escape, \\ included, kept whole
			out = append(out, b[i], b[i+1])
			i++
		}
	}
	return out
}

// jsonKeys lists the keys v encodes as an object, sorted.
func jsonKeys(v any) ([]string, error) {
	b, err := MarshalJSON(v)
	if err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(obj)), nil
}

// unknownFields is one problem per key of obj that is not known, in key
// order, each led by prefix.
func unknownFields(obj map[string]json.RawMessage, prefix string, known ...string) []string {
	var problems []string
	for _, k := range slices.Sorted(maps.Keys(obj)) {
		if !slices.Contains(known, k) {
			problems = append(problems, fmt.Sprintf("%sunknown field '%s'", prefix, k))
		}
	}
	return problems
}

// isNull reports whether a value is absent or null.
func isNull(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || string(raw) == "null"
}

// jsonText is a string, or a number as written; ok is false for anything
// else, null included.
func jsonText(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", false
	}
	switch c := raw[0]; {
	case c == '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return "", false
		}
		return s, true
	case c == '-' || (c >= '0' && c <= '9'):
		return string(raw), true
	}
	return "", false
}

// isText is jsonText that is not empty: what every text field takes.
func isText(raw json.RawMessage) (string, bool) {
	s, ok := jsonText(raw)
	return s, ok && s != ""
}

// jsonString is a JSON string's value; ok is false for any other value.
func jsonString(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	return s, json.Unmarshal(raw, &s) == nil
}

// jsonArray is an array's elements; ok is false for any other value.
func jsonArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	var a []json.RawMessage
	if json.Unmarshal(raw, &a) != nil {
		return nil, false
	}
	return a, true
}

// jsonObject is an object's members; ok is false for any other value.
func jsonObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	var o map[string]json.RawMessage
	if json.Unmarshal(raw, &o) != nil {
		return nil, false
	}
	return o, true
}

// texts is an array whose every element is non-empty text.
func texts(raw json.RawMessage) ([]string, bool) {
	a, ok := jsonArray(raw)
	if !ok {
		return nil, false
	}
	out := make([]string, len(a))
	for i, v := range a {
		if out[i], ok = isText(v); !ok {
			return nil, false
		}
	}
	return out, true
}

// lineValue is a line number: a positive whole number, or a string of
// decimal digits with no leading zero.
func lineValue(raw json.RawMessage) (int, bool) {
	s, ok := jsonText(raw)
	if !ok {
		return 0, false
	}
	n, ok := parsePositive(s)
	return int(n), ok
}

// lineRange reads [start, end]; the problem is "" when it is one.
func lineRange(raw json.RawMessage) (Range, string) {
	a, ok := jsonArray(raw)
	if !ok || len(a) != 2 {
		return Range{}, "must be [start, end]"
	}
	start, ok1 := lineValue(a[0])
	end, ok2 := lineValue(a[1])
	if !ok1 || !ok2 {
		return Range{}, "must be [start, end]"
	}
	if start > end {
		return Range{}, "start is after end"
	}
	return Range{start, end}, ""
}

// compactJSON is a value as one line of JSON, for messages.
func compactJSON(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return string(raw)
	}
	return b.String()
}

// quoteJSON is a string as a JSON string, for messages.
func quoteJSON(s string) string { return MustJSON(s) }
