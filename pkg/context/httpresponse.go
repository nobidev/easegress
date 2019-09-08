package context

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/megaease/easegateway/pkg/logger"
	"github.com/megaease/easegateway/pkg/util/httpheader"
)

var (
	bodyFlushBuffSize = 8 * int64(os.Getpagesize())
)

type (
	// BodyFlushFunc is the type of function to be called back
	// when body is flushing.
	BodyFlushFunc = func(body []byte, complete bool) (newBody []byte)

	httpResponse struct {
		stdr *http.Request
		std  http.ResponseWriter

		code   int
		header *httpheader.HTTPHeader

		body           io.Reader
		bodyWritten    uint64
		bodyFlushFuncs []BodyFlushFunc
	}
)

func newHTTPResponse(stdw http.ResponseWriter, stdr *http.Request) *httpResponse {
	return &httpResponse{
		stdr:   stdr,
		std:    stdw,
		code:   http.StatusOK,
		header: httpheader.New(stdw.Header()),
	}
}

func (w *httpResponse) StatusCode() int {
	return w.code
}

func (w *httpResponse) SetStatusCode(code int) {
	w.code = code
}

func (w *httpResponse) Header() *httpheader.HTTPHeader {
	return w.header
}

func (w *httpResponse) SetCookie(cookie *http.Cookie) {
	http.SetCookie(w.std, cookie)
}

func (w *httpResponse) Body() io.Reader {
	return w.body
}

func (w *httpResponse) SetBody(body io.Reader) {
	w.body = body
}

// None uses it currently, keep it for future maybe.
func (w *httpResponse) OnFlushBody(fn BodyFlushFunc) {
	w.bodyFlushFuncs = append(w.bodyFlushFuncs, fn)
}

func (w *httpResponse) flushBody() {
	if w.body == nil {
		return
	}

	defer func() {
		if body, ok := w.body.(io.ReadCloser); ok {
			// NOTE: Need to be read to completion and closed.
			// Reference: https://golang.org/pkg/net/http/#Response
			err := body.Close()
			if err != nil {
				logger.Warnf("close body failed: %v", err)
			}
		}
	}()

	copyToClient := func(src io.Reader) (succeed bool) {
		written, err := io.Copy(w.std, src)
		if err != nil {
			logger.Warnf("copy body failed: %v", err)
			return false
		}
		w.bodyWritten += uint64(written)
		return true
	}

	if len(w.bodyFlushFuncs) == 0 {
		copyToClient(w.body)
		return
	}

	buff := bytes.NewBuffer(nil)
	for {
		buff.Reset()
		_, err := io.CopyN(buff, w.body, bodyFlushBuffSize)
		body := buff.Bytes()

		switch err {
		case nil:
			// Switch to chunked mode (EaseGateway defined).
			// Reference: https://gist.github.com/CMCDragonkai/6bfade6431e9ffb7fe88
			// NOTE: Golang server will adjust it according to the content length.
			// if !chunkedMode {
			// 	chunkedMode = true
			// 	w.Header().Del("Content-Length")
			// 	w.Header().Set("Transfer-Encoding", "chunked")
			// }

			for _, fn := range w.bodyFlushFuncs {
				body = fn(body, false /*not complete*/)
			}
			if !copyToClient(bytes.NewReader(body)) {
				return
			}
		case io.EOF:
			for _, fn := range w.bodyFlushFuncs {
				body = fn(body, true /*complete*/)
			}

			copyToClient(bytes.NewReader(body))
			return
		default:
			w.SetStatusCode(http.StatusInternalServerError)
			return
		}
	}
}

func (w *httpResponse) FlushedBodyBytes() uint64 {
	return w.bodyWritten
}

func (w *httpResponse) finish() {
	// NOTE: WriteHeader must be called at most one time.
	w.std.WriteHeader(w.StatusCode())
	w.flushBody()
}

func (w *httpResponse) Size() uint64 {
	text := http.StatusText(w.StatusCode())
	if text == "" {
		text = "status code " + strconv.Itoa(w.StatusCode())
	}

	// Reference: https://tools.ietf.org/html/rfc2616#section-6
	// NOTE: We don't use httputil.DumpResponse because it does not
	// completely output plain HTTP Request.
	meta := fmt.Sprintf("%s %d %s\r\n%s\r\n\r\n",
		w.stdr.Proto, w.StatusCode(), text,
		w.Header().Dump())

	return uint64(len(meta)) + w.bodyWritten
}