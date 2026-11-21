package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The catalog holds a translation for every key the app asks for. The test
// reads the app's calls for T("…") and Tf("…") literals and checks each
// against zhCN, so a new string can't ship half-translated.
func TestCatalogCoversTheAppsStrings(t *testing.T) {
	keys := keysFromDir("../app")
	if len(keys) == 0 {
		t.Fatal("found no T() keys in the app; the scan broke")
	}
	SetLanguage(Chinese)
	defer SetLanguage(English)
	for _, k := range keys {
		if _, ok := zhCN[k]; !ok {
			t.Errorf("no Chinese for %q", k)
		}
	}
}

// Resolve speaks the chosen language, and follows the system's preference
// when asked to.
func TestResolve(t *testing.T) {
	if got := Resolve("zh-CN", []string{"fr"}); got != Chinese {
		t.Errorf("Resolve(zh-CN) = %q", got)
	}
	if got := Resolve("system", []string{"zh-Hans-CN", "en"}); got != Chinese {
		t.Errorf("Resolve(system, zh first) = %q", got)
	}
	if got := Resolve("system", []string{"en_US"}); got != English {
		t.Errorf("Resolve(system, en) = %q", got)
	}
	if got := Resolve("system", []string{"fr", "de"}); got != English {
		t.Errorf("Resolve(system, neither) = %q, want the English fallback", got)
	}
}

// T answers the Chinese of a key, and the key itself in English.
func TestT(t *testing.T) {
	SetLanguage(Chinese)
	defer SetLanguage(English)
	if got := T("Load More"); got != "加载更多" {
		t.Errorf("T in Chinese = %q", got)
	}
	SetLanguage(English)
	if got := T("Load More"); got != "Load More" {
		t.Errorf("T in English = %q", got)
	}
}

// keysFromDir collects the string literals of every T( and Tf( call in the
// package's .go files, outside its own folder.
func keysFromDir(dir string) []string {
	var keys []string
	paths, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	re := regexp.MustCompile(`i18n\.T(?:f)?\("([^"]+)"`)
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			// The literal is as the source spells it, escapes and all; the
			// key is what the string says at runtime.
			if unquoted, err := strconv.Unquote("\"" + m[1] + "\""); err == nil {
				keys = append(keys, unquoted)
			}
		}
	}
	return keys
}
