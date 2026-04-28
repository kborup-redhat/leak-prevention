package matcher

import (
	_ "embed"
	"strings"
)

//go:embed words.txt
var wordsRaw string

var dictionary map[string]bool

func init() {
	dictionary = make(map[string]bool, 120000)
	for _, word := range strings.Split(wordsRaw, "\n") {
		if word != "" {
			dictionary[strings.ToLower(word)] = true
		}
	}
}

func IsEnglishWord(word string) bool {
	return dictionary[strings.ToLower(word)]
}
