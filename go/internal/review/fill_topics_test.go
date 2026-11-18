package review

import (
	"encoding/json"
	"testing"
)

func TestSameTopics(t *testing.T) {
	stored := []Topic{{Name: "A", Paths: []string{"a", "b"}}, {Name: "B", Paths: []string{"c"}}}
	for in, want := range map[string]bool{
		`[{"name": "A", "paths": ["b", "a"]}, {"name": "B", "paths": ["c"]}]`:         true,
		`[{"name": "B", "paths": ["c"]}, {"name": "A", "paths": ["a", "b"]}]`:         false,
		`[{"name": "A", "paths": ["a"]}, {"name": "B", "paths": ["b", "c"]}]`:         false,
		`[{"name": "A", "paths": ["a", "b"], "x": 1}, {"name": "B", "paths": ["c"]}]`: false,
		`[{"name": "A ", "paths": ["a", "b"]}, {"name": "B", "paths": ["c"]}]`:        false,
		`{"name": "A"}`: false,
	} {
		if got := sameTopics(json.RawMessage(in), stored); got != want {
			t.Errorf("%s: got %v", in, got)
		}
	}
	if sameTopics(json.RawMessage(`[]`), nil) {
		t.Error("nothing stored is never the same")
	}
}
