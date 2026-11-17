package pluginhost

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httputil"
	"net/textproto"
	"net/url"
	"slices"
	"strconv"
	"syscall"
)

// strippedHeaders never reach a plugin: the browser's credentials stay with
// the cockpit, the plugin sits behind its session.
var strippedHeaders = []string{"Cookie", "Authorization", "Proxy-Authorization", "X-CSRF-Token"}

// ServeHTTP proxies one request of the plugin's URL subtree to its socket,
// the path relative to the subtree as a compiled plugin's handler sees it.
// A plugin that is not running is refused without dialing its socket. The
// default FlushInterval stays: a stream flushes every write, a body of known
// length is not flushed, so the cockpit's gzip sees the length before the
// header goes out.
func (s *supervisor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	transport := s.transport
	if s.state != StateRunning {
		transport = nil
	}
	s.mu.Unlock()
	if transport == nil {
		http.Error(w, s.name+" is not running.", http.StatusServiceUnavailable)
		return
	}
	proxy := &httputil.ReverseProxy{
		Rewrite:        s.rewrite,
		Transport:      transport,
		ModifyResponse: dropResponseCredentials,
		ErrorHandler:   s.proxyError,
	}
	proxy.ServeHTTP(w, r)
}

func (s *supervisor) rewrite(pr *httputil.ProxyRequest) {
	pr.Out.URL.Scheme = "http"
	pr.Out.URL.Host = "plugin"
	pr.Out.Host = "plugin"
	pr.SetXForwarded()
	pr.Out.Header.Set("X-Forwarded-Prefix", "/plugins/"+s.id)
	for _, name := range strippedHeaders {
		pr.Out.Header.Del(name)
	}
	stripFormToken(pr.In, pr.Out, s.host.cfg.CSRFField)
}

// stripFormToken rebuilds a form body without the CSRF token field, even
// when the token came as a header: the token never reaches a plugin. It works
// from the parsed form, the CSRF check may have consumed the body. Any other
// body passes untouched.
func stripFormToken(in, out *http.Request, field string) {
	if in.Method == http.MethodGet || in.Method == http.MethodHead || field == "" {
		return
	}
	mediaType, _, _ := mime.ParseMediaType(in.Header.Get("Content-Type"))
	switch mediaType {
	case "multipart/form-data":
		if in.MultipartForm == nil {
			_ = in.ParseMultipartForm(32 << 20)
		}
	case "application/x-www-form-urlencoded":
		if in.PostForm == nil {
			_ = in.ParseForm()
		}
	default:
		return
	}
	var body []byte
	switch {
	case in.MultipartForm != nil && mediaType == "multipart/form-data":
		var contentType string
		var err error
		body, contentType, err = rebuildMultipart(in.MultipartForm, field)
		if err != nil {
			body, contentType = nil, "text/plain"
		}
		out.Header.Set("Content-Type", contentType)
	case in.PostForm != nil && mediaType == "application/x-www-form-urlencoded":
		values := url.Values{}
		for key, list := range in.PostForm {
			if key != field {
				values[key] = list
			}
		}
		body = []byte(values.Encode())
	default:
		return
	}
	out.Body = io.NopCloser(bytes.NewReader(body))
	out.ContentLength = int64(len(body))
	out.Header.Set("Content-Length", strconv.Itoa(len(body)))
	out.GetBody = nil
}

func rebuildMultipart(form *multipart.Form, field string) ([]byte, string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for _, key := range sortedKeys(form.Value) {
		if key == field {
			continue
		}
		for _, value := range form.Value[key] {
			if err := writer.WriteField(key, value); err != nil {
				return nil, "", err
			}
		}
	}
	for _, key := range sortedKeys(form.File) {
		for _, header := range form.File[key] {
			partHeader := textproto.MIMEHeader{}
			partHeader.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": key, "filename": header.Filename}))
			if contentType := header.Header.Get("Content-Type"); contentType != "" {
				partHeader.Set("Content-Type", contentType)
			}
			part, err := writer.CreatePart(partHeader)
			if err != nil {
				return nil, "", err
			}
			file, err := header.Open()
			if err != nil {
				return nil, "", err
			}
			_, err = io.Copy(part, file)
			_ = file.Close()
			if err != nil {
				return nil, "", err
			}
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), writer.FormDataContentType(), nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// dropResponseCredentials keeps a plugin from setting or clearing cookies,
// it shares the cockpit's origin and would reach the session.
func dropResponseCredentials(res *http.Response) error {
	res.Header.Del("Set-Cookie")
	res.Header.Del("Clear-Site-Data")
	return nil
}

// proxyError answers a failed proxy request with one plain sentence.
func (s *supervisor) proxyError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	var netErr net.Error
	switch {
	case errors.As(err, &netErr) && netErr.Timeout():
		http.Error(w, s.name+" did not answer in time.", http.StatusGatewayTimeout)
	case errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, fs.ErrNotExist):
		http.Error(w, s.name+" is not running.", http.StatusServiceUnavailable)
	default:
		http.Error(w, s.name+" sent an answer that is not HTTP.", http.StatusBadGateway)
	}
}
