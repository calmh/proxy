package proxy

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/golang/groupcache/lru"
	"golang.org/x/sync/singleflight"
)

type Proxy struct {
	ttl    time.Duration
	next   http.Handler
	single *singleflight.Group
	cache  *lru.Cache
}

func New(ttl time.Duration, size int, next http.Handler) *Proxy {
	return &Proxy{
		ttl:    ttl,
		next:   next,
		single: new(singleflight.Group),
		cache:  lru.New(size),
	}
}

type response struct {
	code   int
	header http.Header
	body   []byte
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Generate cache key with time window
	key := fmt.Sprintf("%s-%s-%d", req.Method, req.URL.String(), time.Now().Truncate(p.ttl).Unix())

	// Check cache
	if resp, ok := p.cache.Get(key); ok {
		r := resp.(response)
		writeResponse(w, r)
		return
	}

	// Fetch data
	resp, err, _ := p.single.Do(key, func() (interface{}, error) {
		wr := &httptest.ResponseRecorder{
			Body: new(bytes.Buffer),
		}
		p.next.ServeHTTP(wr, req)

		r := response{
			code:   wr.Code,
			header: wr.Result().Header,
			body:   wr.Body.Bytes(),
		}

		// Cache response
		if r.code == http.StatusOK && (req.Method == http.MethodGet || req.Method == http.MethodHead || req.Method == http.MethodOptions) {
			r.header.Set("Cache-Control", fmt.Sprintf("max-age=%d", int(p.ttl.Seconds())))
			r.header.Set("Expires", time.Now().Add(p.ttl).Format(time.RFC1123))
			r.header.Set("X-Cache-Date", r.header.Get("Date"))
			r.header.Del("Date")
			p.cache.Add(key, r)
		}

		return r, nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeResponse(w, resp.(response))
}

func writeResponse(w http.ResponseWriter, r response) {
	for k, v := range r.header {
		w.Header()[k] = v
	}
	w.WriteHeader(r.code)
	w.Write(r.body) //nolint:errcheck
}
