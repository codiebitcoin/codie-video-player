// Package i18n says which language the listener speaks, so plugins can
// translate their names and their errors: en, or zh-CN.
package i18n

import "sync"

const (
	English = "en"
	Chinese = "zh-CN"
)

var (
	mu   sync.Mutex
	code = English
)

// SetLanguage speaks another language from now on.
func SetLanguage(c string) {
	mu.Lock()
	code = c
	mu.Unlock()
}

// Language is the language the listener speaks.
func Language() string {
	mu.Lock()
	defer mu.Unlock()
	return code
}

// Languages are the ones the app speaks, first the system's preferred.
var Languages = []struct{ Code string }{{English}, {Chinese}}

// T is the text in the listener's language: the app's own strings are written
// in Chinese straight out, so it answers as it is.
func T(text string) string { return text }
