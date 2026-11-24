// Package i18n is the app's languages. The English text is the key: T returns
// it as it is in English, and its translation in any other language. Keys are
// literal strings at the call, for a test to find them all.
package i18n

import (
	"fmt"
	"strings"
	"sync/atomic"
)

const (
	// System is the setting of following the system's language: see Resolve.
	System   = "system"
	English  = "en"
	Chinese  = "zh-CN"
	Japanese = "ja"
)

// Languages are those the viewer can choose, each named in itself.
var Languages = []struct{ Code, Name string }{
	{English, "English"},
	{Chinese, "简体中文"},
	{Japanese, "日本語"},
}

// Resolve is the language to speak for a setting: the one chosen, or for System,
// the first of those the system prefers that the app has, or else English.
func Resolve(setting string, preferred []string) string {
	if setting == English || setting == Chinese || setting == Japanese {
		return setting
	}
	for _, tag := range preferred {
		base, _, _ := strings.Cut(strings.ToLower(strings.ReplaceAll(tag, "_", "-")), "-")
		switch base {
		case "zh":
			return Chinese
		case "ja":
			return Japanese
		case "en":
			return English
		}
	}
	return English
}

var language atomic.Pointer[string]

// SetLanguage switches the language of what T returns from now on. A language
// the app doesn't have is English.
func SetLanguage(code string) {
	if code != Chinese && code != Japanese {
		code = English
	}
	language.Store(&code)
}

// Language returns the language T translates to.
func Language() string {
	if l := language.Load(); l != nil {
		return *l
	}
	return English
}

// T returns the text in the viewer's language.
func T(text string) string {
	switch Language() {
	case Chinese:
		if zh, ok := zhCN[text]; ok {
			return zh
		}
	case Japanese:
		if ja, ok := jaJP[text]; ok {
			return ja
		}
	}
	return text
}

// Tf is T of a format, formatted. Its arguments must stay in the order the
// English has them, or the translation numbers them, as %[2]d.
func Tf(format string, args ...any) string {
	return fmt.Sprintf(T(format), args...)
}

// N marks text for translation where it's kept to show later, through T, as
// in a menu built once: it returns it as it is.
func N(text string) string { return text }

// Count says a count shortly, as people read it in the language.
func Count(n int) string {
	if Language() == Chinese {
		switch {
		case n >= 1e8:
			return fmt.Sprintf("%.1f亿", float64(n)/1e8)
		case n >= 1e4:
			return fmt.Sprintf("%.1f万", float64(n)/1e4)
		}
		return fmt.Sprint(n)
	}
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}
