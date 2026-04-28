package matcher

import "testing"

func TestIsEnglishWord_CommonWords(t *testing.T) {
	words := []string{"hello", "world", "computer", "running", "development"}
	for _, w := range words {
		if !IsEnglishWord(w) {
			t.Errorf("expected %q to be in dictionary", w)
		}
	}
}

func TestIsEnglishWord_NotInDictionary(t *testing.T) {
	words := []string{"xyzzy", "asdfgh", "qwerty123"}
	for _, w := range words {
		if IsEnglishWord(w) {
			t.Errorf("expected %q to NOT be in dictionary", w)
		}
	}
}

func TestIsEnglishWord_CaseInsensitive(t *testing.T) {
	if !IsEnglishWord("Hello") {
		t.Error("dictionary lookup should be case-insensitive")
	}
}
