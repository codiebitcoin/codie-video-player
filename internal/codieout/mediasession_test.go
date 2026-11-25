package codieout

import (
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestCreateMediaSessionStreamsFile checks the upload two ways at once: the
// file's bytes reach the device whole even though they stream from disk
// rather than a buffer, and a screen that's taken (MEDIA_SESSION_BLOCKED)
// is freed and asked again with the body re-read from the start.
func TestCreateMediaSessionStreamsFile(t *testing.T) {
	body := make([]byte, 300_000)
	for i := range body {
		body[i] = byte(i % 251)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "seg-001.mp4")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	var posts atomic.Int64
	var freed atomic.Bool
	var gotFile []byte
	var gotRequest MediaSessionRequest
	var gotLen int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/media/sessions"):
			if posts.Add(1) == 1 {
				// 第一次:屏幕被占着。
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				io.WriteString(w, `{"code":"MEDIA_SESSION_BLOCKED","message":"busy"}`)
				return
			}
			gotLen = r.ContentLength
			mr, err := readFields(multipart.NewReader(r.Body, boundaryOf(r)))
			if err != nil {
				t.Errorf("multipart: %v", err)
				return
			}
			if err := json.Unmarshal([]byte(mr.Value("request")), &gotRequest); err != nil {
				t.Errorf("request part: %v", err)
			}
			gotFile = mr.FileBytes("file")
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"code":"OK","data":{"session_id":"s-1"}}`)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/media/sessions"):
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"code":"OK","data":{"sessions":[{"session_id":"held"}]}}`)
		case r.Method == "DELETE" && strings.HasSuffix(r.URL.Path, "/media/sessions/held"):
			freed.Store(true)
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"code":"OK","data":{"session_id":"held"}}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := New(&Creds{Addr: strings.TrimPrefix(srv.URL, "http://"), Secret: "k", UserID: "u1"})
	session, err := c.CreateMediaSession(path, MediaSessionRequest{})
	if err != nil {
		t.Fatalf("CreateMediaSession: %v", err)
	}
	if session != "s-1" {
		t.Fatalf("session = %q, want s-1", session)
	}
	if posts.Load() != 2 {
		t.Fatalf("posts = %d, want the blocked first try and a retry", posts.Load())
	}
	if !freed.Load() {
		t.Error("the session holding the screen wasn't freed before the retry")
	}
	if gotRequest.ConflictPolicy != "replace" {
		t.Errorf("conflict policy = %q, want the default replace", gotRequest.ConflictPolicy)
	}
	if string(gotFile) != string(body) {
		t.Errorf("the file arrived %d bytes, want the whole %d", len(gotFile), len(body))
	}
	// The body declared its length up front: no chunked guessing for a
	// device that may not read it that way.
	if want := gotLen; want > 0 && want <= int64(len(body)) {
		t.Errorf("Content-Length = %d, want more than the file alone (envelope on top)", want)
	}
}

// fields is one multipart body, its parts kept by name.
type fields struct {
	values map[string]string
	files  map[string][]byte
}

func (f *fields) Value(name string) string { return f.values[name] }
func (f *fields) FileBytes(name string) []byte {
	return f.files[name]
}

// readFields reads every part of a multipart body into memory, by name.
func readFields(r *multipart.Reader) (*fields, error) {
	f := &fields{values: map[string]string{}, files: map[string][]byte{}}
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			return f, nil
		}
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(p)
		if err != nil {
			return nil, err
		}
		if p.FileName() == "" {
			f.values[p.FormName()] = string(b)
		} else {
			f.files[p.FormName()] = b
		}
	}
}

// boundaryOf is the multipart boundary a request declared.
func boundaryOf(r *http.Request) string {
	for _, v := range r.Header["Content-Type"] {
		if i := strings.Index(v, "boundary="); i >= 0 {
			return strings.Trim(v[i+len("boundary="):], `"`)
		}
	}
	return ""
}
