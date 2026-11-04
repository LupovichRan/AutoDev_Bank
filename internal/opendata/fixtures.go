package opendata

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
)

// FixtureClient returns an *http.Client that never touches the network:
// each request is answered with the file routes[host+path] read from fsys
// (query strings are ignored), or 404 when no route matches. The concrete
// route table lives with the providers (providers.FixtureProviders); this
// helper sits here so opendata does not import its own providers package.
func FixtureClient(fsys fs.FS, routes map[string]string) *http.Client {
	return &http.Client{Transport: fixtureTransport{fsys: fsys, routes: routes}}
}

type fixtureTransport struct {
	fsys   fs.FS
	routes map[string]string
}

func (t fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	resp := &http.Response{
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{}, Request: req,
	}
	name, ok := t.routes[req.URL.Host+req.URL.Path]
	if !ok {
		resp.StatusCode, resp.Status = http.StatusNotFound, "404 Not Found"
		resp.Body = io.NopCloser(bytes.NewReader([]byte("no fixture for " + req.URL.Host + req.URL.Path)))
		return resp, nil
	}
	b, err := fs.ReadFile(t.fsys, name)
	if err != nil {
		return nil, fmt.Errorf("fixture %s: %w", name, err)
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	resp.StatusCode, resp.Status = http.StatusOK, "200 OK"
	resp.Header.Set("Content-Type", ct)
	resp.ContentLength = int64(len(b))
	resp.Body = io.NopCloser(bytes.NewReader(b))
	return resp, nil
}
