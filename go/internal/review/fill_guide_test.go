package review

import "testing"

func TestCodeTokens(t *testing.T) {
	for text, code := range map[string]bool{
		"Calls foo with the list.":  false,
		"Costs 5 percent, 100%.":    false,
		"Uses `foo`.":               true,
		"Then a -> b.":              true,
		"Foo::Bar loads.":           true,
		"Call foo() first.":         true,
		"Reads $x.":                 true,
		"Mails a@b.":                true,
		"Takes %é first.":           true,
		"Says $ alone, and @ here.": false,
	} {
		if got := codeToken.MatchString(text); got != code {
			t.Errorf("%q: code %v", text, got)
		}
	}
}
