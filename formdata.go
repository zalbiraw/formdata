// Package formdata implements a plugin to mutate request form data.
package formdata

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

// Config the plugin configuration.
type Config struct {
	// Set: create or update form values
	Set map[string]string `json:"set,omitempty" yaml:"set,omitempty"`
	// Append: add additional form values (does not replace)
	Append map[string]string `json:"append,omitempty" yaml:"append,omitempty"`
	// Delete: list of form keys to remove
	Delete []string `json:"delete,omitempty" yaml:"delete,omitempty"`
}

// CreateConfig creates the default plugin configuration.
func CreateConfig() *Config {
	return &Config{
		Set:    make(map[string]string),
		Append: make(map[string]string),
		Delete: []string{},
	}
}

// Formdata represents the formdata plugin.
type Formdata struct {
	next     http.Handler
	set      map[string]string
	appendTo map[string]string
	delete   []string
	name     string
}

// New created a new Formdata plugin.
func New(ctx context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	if len(config.Set) == 0 && len(config.Append) == 0 && len(config.Delete) == 0 {
		return nil, fmt.Errorf("at least one of set, append, or delete must be provided")
	}

	return &Formdata{
		set:      config.Set,
		appendTo: config.Append,
		delete:   config.Delete,
		next:     next,
		name:     name,
	}, nil
}

func (a *Formdata) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	ct := req.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "application/x-www-form-urlencoded"):
		a.handleURLEncoded(rw, req)
	case strings.HasPrefix(ct, "multipart/form-data"):
		a.handleMultipart(rw, req)
	}
	a.next.ServeHTTP(rw, req)
}

// handleURLEncoded mutates application/x-www-form-urlencoded request bodies in-place.
func (a *Formdata) handleURLEncoded(rw http.ResponseWriter, req *http.Request) {
	if err := req.ParseForm(); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	form := req.PostForm

	for _, k := range a.delete {
		form.Del(k)
	}
	for k, v := range a.set {
		form.Set(k, v)
	}
	for k, v := range a.appendTo {
		form.Add(k, v)
	}

	encoded := form.Encode()
	req.Body = io.NopCloser(strings.NewReader(encoded))
	req.ContentLength = int64(len(encoded))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(encoded)), nil }
}

// handleMultipart mutates multipart/form-data request bodies while preserving file parts.
func (a *Formdata) handleMultipart(rw http.ResponseWriter, req *http.Request) {
	if err := req.ParseMultipartForm(32 << 20); err != nil { // 32MB memory threshold
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	m := req.MultipartForm
	if m == nil {
		return
	}

	// Apply operations to values
	a.applyOpsToValues(m.Value)

	// Rebuild body
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writeValues(writer, m.Value); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := writeFiles(writer, m.File); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := writer.Close(); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	req.Body = io.NopCloser(&body)
	req.ContentLength = int64(body.Len())
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+writer.Boundary())
	snapshot := body.Bytes()
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(snapshot)), nil }
}

// applyOpsToValues applies delete, set, and append operations to the given values map.
func (a *Formdata) applyOpsToValues(values map[string][]string) {
	for _, k := range a.delete {
		delete(values, k)
	}
	for k, v := range a.set {
		values[k] = []string{v}
	}
	for k, v := range a.appendTo {
		values[k] = append(values[k], v)
	}
}

// writeValues writes simple form values to a multipart writer.
func writeValues(w *multipart.Writer, values map[string][]string) error {
	for k, vals := range values {
		for _, v := range vals {
			if err := w.WriteField(k, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeFiles writes file fields to a multipart writer.
func writeFiles(w *multipart.Writer, files map[string][]*multipart.FileHeader) error {
	for field, fhs := range files {
		for _, fh := range fhs {
			f, err := fh.Open()
			if err != nil {
				return err
			}
			part, err := w.CreateFormFile(field, fh.Filename)
			if err != nil {
				_ = f.Close()
				return err
			}
			if _, err := io.Copy(part, f); err != nil {
				_ = f.Close()
				return err
			}
			_ = f.Close()
		}
	}
	return nil
}
