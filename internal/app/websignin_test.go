package app

import (
	"slices"
	"testing"

	"github.com/egoist/mygo"
)

func TestCookieHeader(t *testing.T) {
	cookies := []mygo.Cookie{
		{Name: "sessid", Value: "new"},
		{Name: "ss", Value: "same"},
	}
	tests := []struct {
		name    string
		cookies []mygo.Cookie
		until   []string
		changed []string
		was     map[string]string
		ok      bool
		header  string
	}{
		{
			name:    "no condition is a header of them all",
			cookies: cookies,
			ok:      true,
			header:  "sessid=new; ss=same",
		},
		{
			name:    "until waits for each name",
			cookies: cookies,
			until:   []string{"sessid", "user_session"},
		},
		{
			name:    "until met answers",
			cookies: cookies,
			until:   []string{"sessid"},
			ok:      true,
			header:  "sessid=new; ss=same",
		},
		{
			name:    "without the first values nothing is compared yet",
			cookies: cookies,
			changed: []string{"sessid"},
		},
		{
			name:    "a value that changed answers",
			cookies: cookies,
			changed: []string{"sessid"},
			was:     map[string]string{"sessid": "old"},
			ok:      true,
			header:  "sessid=new; ss=same",
		},
		{
			name:    "a value that didn't change is still waiting",
			cookies: cookies,
			changed: []string{"sessid", "ss"},
			was:     map[string]string{"sessid": "new", "ss": "same"},
		},
		{
			name:    "any one of them changing is enough",
			cookies: cookies,
			changed: []string{"sessid", "ss"},
			was:     map[string]string{"sessid": "new", "ss": "old"},
			ok:      true,
			header:  "sessid=new; ss=same",
		},
		{
			name:    "a cookie that appeared is a change too",
			cookies: append(slices.Clone(cookies), mygo.Cookie{Name: "PHPSESSID", Value: "x"}),
			changed: []string{"PHPSESSID"},
			was:     map[string]string{},
			ok:      true,
			header:  "sessid=new; ss=same; PHPSESSID=x",
		},
		{
			name:    "one that hasn't appeared is still waiting",
			cookies: cookies,
			changed: []string{"PHPSESSID"},
			was:     map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header, ok := cookieHeader(tt.cookies, tt.until, tt.changed, tt.was)
			if ok != tt.ok || header != tt.header {
				t.Errorf("cookieHeader = %q, %v; want %q, %v", header, ok, tt.header, tt.ok)
			}
		})
	}
}
