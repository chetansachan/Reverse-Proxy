package pool

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// NewTransport creates an optimized HTTP Transport tailored for high-concurrency reverse proxying.
func NewTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          5000,
		MaxIdleConnsPerHost:   1000,
		MaxConnsPerHost:       2000,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     false,
	}
}

const DefaultBufferSize = 32 * 1024 // 32 KB

// BufferPool implements httputil.BufferPool using sync.Pool to recycle byte buffers.
type BufferPool struct {
	pool sync.Pool
}

// NewBufferPool creates a reusable 32KB BufferPool.
func NewBufferPool() *BufferPool {
	return &BufferPool{
		pool: sync.Pool{
			New: func() any {
				b := make([]byte, DefaultBufferSize)
				return &b
			},
		},
	}
}

// Get fetches a byte buffer from the pool.
func (bp *BufferPool) Get() []byte {
	bufPtr := bp.pool.Get().(*[]byte)
	return *bufPtr
}

// Put returns the byte buffer to the pool.
func (bp *BufferPool) Put(b []byte) {
	if cap(b) >= DefaultBufferSize {
		b = b[:DefaultBufferSize]
		bp.pool.Put(&b)
	}
}
