package longpoll

import (
	"log"
	"net"
	"net/http"
	"sync"
)

type Longpoller struct {
	// Logger for errors. If nil the logging package's standard logger is used.
	Logger *log.Logger

	nextid int64

	mu    *sync.Mutex
	conns map[int64]*conn
}

func New() *Longpoller {
	return &Longpoller{nil, 0, &sync.Mutex{}, map[int64]*conn{}}
}

type conn struct {
	nc   net.Conn
	done chan bool
}

// Writing to the Longpoller will write to each connection currently managed by
// the Longpoller. If an error occurs, the connection is closed. the err
// returned by this function is always nil.
func (lp *Longpoller) Write(p []byte) (n int, err error) {
	lp.mu.Lock()
	for id, c := range lp.conns {
		if _, err := c.nc.Write(p); err != nil {
			delete(lp.conns, id)
			c.done <- true
		}
	}
	lp.mu.Unlock()
	return len(p), nil
}

var header = http.Header{
	"Content-Type":  {"text/event-stream"},
	"Cache-Control": {"no-cache"},
	"Connection":    {"keep-alive"},
}

// ServeHTTP hijacks the underlying net connection and holds it for writes from
// the Longpoller.
func (lp *Longpoller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Could not process request", http.StatusInternalServerError)
		lp.logf("longpoll: received a response writer which was not a hijacker")
		return
	}

	nc, _, err := hj.Hijack()
	if err != nil {
		lp.logf("longpoll: hijacking error: %v", err)
	}
	defer nc.Close()

	// Write a response header to the request
	err = (&http.Response{
		Status:        http.StatusText(http.StatusOK),
		StatusCode:    http.StatusOK,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		ContentLength: -1,
		Request:       r,
	}).Write(nc)

	if err != nil {
		lp.logf("longpoll: error writing the response header: %v", err)
		return
	}

	done := make(chan bool, 1)

	lp.mu.Lock()

	id := lp.nextid
	lp.nextid++
	lp.conns[id] = &conn{nc, done}

	lp.mu.Unlock()

	<-done
}

func (lp *Longpoller) logf(f string, a ...interface{}) {
	var printf func(f string, a ...interface{})
	if lp.Logger == nil {
		printf = log.Printf
	} else {
		printf = lp.Logger.Printf
	}
	printf(f, a...)
}
