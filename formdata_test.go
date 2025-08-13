package formdata

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// nextHandler captures the request seen downstream for assertions.
type nextHandler struct {
	req *http.Request
}

func (h *nextHandler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	// Read the body fully so it can be re-used by tests if needed
	if r.Body != nil {
		_, _ = io.ReadAll(r.Body)
	}
	h.req = r
	rw.WriteHeader(http.StatusOK)
}

// ----- test helpers (reduce cyclomatic complexity in tests) -----

func assertStatusOK(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rr.Code)
	}
}

func mustDownstream(t *testing.T, next *nextHandler) *http.Request {
	t.Helper()
	if next.req == nil {
		t.Fatalf("downstream request not captured")
	}
	return next.req
}

func mustParseForm(t *testing.T, r *http.Request) {
	t.Helper()
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm downstream: %v", err)
	}
}

func mustParseMultipart(t *testing.T, r *http.Request) *multipart.Form {
	t.Helper()
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		t.Fatalf("ParseMultipartForm: %v", err)
	}
	if r.MultipartForm == nil {
		t.Fatalf("downstream MultipartForm nil")
	}
	return r.MultipartForm
}

func assertNoKey(t *testing.T, vals url.Values, key string) {
	t.Helper()
	if _, ok := vals[key]; ok {
		t.Fatalf("expected key %q to be deleted, got %v", key, vals[key])
	}
}

func assertPostFormEquals(t *testing.T, vals url.Values, key string, expected []string) {
	t.Helper()
	got := vals[key]
	if len(got) != len(expected) {
		t.Fatalf("unexpected %s length: got %d want %d (%#v)", key, len(got), len(expected), got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Fatalf("unexpected %s at %d: got %q want %q (%#v)", key, i, got[i], expected[i], got)
		}
	}
}

func replayBody(t *testing.T, r *http.Request) string {
	t.Helper()
	if r.GetBody == nil {
		t.Fatalf("GetBody not set")
	}
	rc, err := r.GetBody()
	if err != nil {
		t.Fatalf("GetBody err: %v", err)
	}
	b, _ := io.ReadAll(rc)
	return string(b)
}

func assertMultipartValues(t *testing.T, vals map[string][]string, key string, expected []string) {
	t.Helper()
	got := vals[key]
	if len(got) != len(expected) {
		t.Fatalf("unexpected %s length: got %d want %d (%#v)", key, len(got), len(expected), got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Fatalf("unexpected %s at %d: got %q want %q (%#v)", key, i, got[i], expected[i], got)
		}
	}
}

func assertContentTypeMultipart(t *testing.T, r *http.Request) {
	t.Helper()
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "multipart/form-data; boundary=") {
		t.Fatalf("unexpected content-type: %s", ct)
	}
}

func assertFilePreserved(t *testing.T, files map[string][]*multipart.FileHeader, field, expected string) {
	t.Helper()
	fhs := files[field]
	if len(fhs) != 1 {
		t.Fatalf("expected 1 file, got %d", len(fhs))
	}
	fh := fhs[0]
	f, err := fh.Open()
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	content, _ := io.ReadAll(f)
	_ = f.Close()
	if string(content) != expected {
		t.Fatalf("unexpected file content: %q", string(content))
	}
}

func TestNew_EmptyConfigError(t *testing.T) {
	cfg := &Config{}
	next := &nextHandler{}
	_, err := New(context.Background(), next, cfg, "test")
	if err == nil {
		t.Fatalf("expected error for empty config, got nil")
	}
}

func TestServeHTTP_URLEncoded_FormMutations(t *testing.T) {
	cfg := &Config{
		Delete: []string{"a"},
		Set:    map[string]string{"b": "y", "c": "z"},
		Append: map[string]string{"b": "w", "d": "q"},
	}
	next := &nextHandler{}
	h, err := New(context.Background(), next, cfg, "test")
	if err != nil {
		t.Fatalf("New error: %v", err)
	}

	form := url.Values{}
	form.Add("a", "1")
	form.Add("a", "2")
	form.Set("b", "x")
	body := form.Encode()
	req := httptest.NewRequest(http.MethodPost, "http://example/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.ContentLength = int64(len(body))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assertStatusOK(t, rec)

	down := mustDownstream(t, next)
	mustParseForm(t, down)
	assertNoKey(t, down.PostForm, "a")
	assertPostFormEquals(t, down.PostForm, "b", []string{"y", "w"})
	assertPostFormEquals(t, down.PostForm, "d", []string{"q"})
	if down.PostForm.Get("c") != "z" {
		t.Fatalf("unexpected c: %q", down.PostForm.Get("c"))
	}

	replayed := replayBody(t, down)
	if !strings.Contains(replayed, "b=y") || !strings.Contains(replayed, "b=w") || !strings.Contains(replayed, "c=z") || strings.Contains(replayed, "a=") {
		t.Fatalf("unexpected replayed body: %s", replayed)
	}
}

func TestServeHTTP_Multipart_ValuesMutations(t *testing.T) {
	cfg := &Config{
		Delete: []string{"a"},
		Set:    map[string]string{"b": "y"},
		Append: map[string]string{"b": "w", "d": "q"},
	}
	next := &nextHandler{}
	h, err := New(context.Background(), next, cfg, "test")
	if err != nil {
		t.Fatalf("New error: %v", err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if werr := mw.WriteField("a", "1"); werr != nil {
		t.Fatalf("write field: %v", werr)
	}
	if werr := mw.WriteField("a", "2"); werr != nil {
		t.Fatalf("write field: %v", werr)
	}
	if werr := mw.WriteField("b", "x"); werr != nil {
		t.Fatalf("write field: %v", werr)
	}
	// add a file too
	fw, err := mw.CreateFormFile("file", "hello.txt")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write([]byte("hello world")); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close mw: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://example/", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+mw.Boundary())
	req.ContentLength = int64(buf.Len())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assertStatusOK(t, rec)

	down := mustDownstream(t, next)
	m := mustParseMultipart(t, down)
	if _, ok := m.Value["a"]; ok {
		t.Fatalf("expected 'a' deleted")
	}
	assertMultipartValues(t, m.Value, "b", []string{"y", "w"})
	assertMultipartValues(t, m.Value, "d", []string{"q"})
	assertContentTypeMultipart(t, down)
}

func TestServeHTTP_Multipart_FilePreserved(t *testing.T) {
	cfg := &Config{Set: map[string]string{"x": "1"}}
	next := &nextHandler{}
	h, err := New(context.Background(), next, cfg, "test")
	if err != nil {
		t.Fatalf("New error: %v", err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "hello.txt")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write([]byte("hello world")); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close mw: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://example/", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+mw.Boundary())
	req.ContentLength = int64(buf.Len())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assertStatusOK(t, rec)
	down := mustDownstream(t, next)
	m := mustParseMultipart(t, down)
	assertFilePreserved(t, m.File, "file", "hello world")
	// replay body not empty
	if len(replayBody(t, down)) == 0 {
		t.Fatalf("replayed body empty")
	}
}
