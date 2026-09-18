# High-Performance Reverse Proxy & Load Balancer - Architecture & Blueprint

A complete from-scratch guide explaining the logic, architecture, algorithms, and Go standard library packages used in this project. Use this document as a blueprint to understand or recreate this reverse proxy from scratch.

---

## 1. High-Level Concept

A **Reverse Proxy & Load Balancer** sits between client applications (browsers, mobile apps, curl) and upstream backend servers.

```
                  +--------------------------------------------------+
                  |               Reverse Proxy (:8080)             |
                  |                                                  |
                  |  1. Incoming Request (HTTP)                     |
                  |  2. Structured Logging Middleware                |
                  |  3. Strategy Selection (RoundRobin/LeastConn/IP) |
                  |  4. Connection & Buffer Pool (Zero-alloc copy)   |
[Client Request] ----> 5. Header Enrichment (X-Forwarded-For, etc.)   |
                  |  6. Active & Passive Health Checking             |
                  |  7. Atomic Balancer Hot-Reload                   |
                  +-------------------------+------------------------+
                                            |
                  +-------------------------+------------------------+
                  |                         |                        |
                  v                         v                        v
          [Backend 1 :8081]         [Backend 2 :8082]        [Backend 3 :8083]
```

### Core Responsibilities:
1. **Traffic Routing & Load Balancing**: Distribute incoming HTTP requests evenly across multiple backend instances.
2. **Fault Tolerance & Health Checking**: Detect dead servers and route traffic only to healthy instances.
3. **High Concurrency & Memory Efficiency**: Utilize connection pooling (`http.Transport`) and memory recycling (`sync.Pool` / `httputil.BufferPool`) to handle thousands of requests per second without GC stalls.
4. **Zero-Downtime Hot Reload**: Swap configuration and balancer strategies at runtime without restarting the process or dropping active connections.
5. **Graceful Shutdown**: Drain in-flight requests cleanly upon receiving OS termination signals (`SIGINT`, `SIGTERM`).

---

## 2. Standard Library & External Packages (Documentation Index)

When rebuilding this project, refer to the documentation for these packages:

### Go Standard Library Packages
| Package | Why It Is Used | Key Concepts / Types to Read |
| :--- | :--- | :--- |
| **`net/http`** | Core HTTP server, client, handler interface, timeouts, and status codes. | `http.Handler`, `http.ResponseWriter`, `*http.Request`, `*http.Server`, `http.ServeMux` |
| **`net/http/httputil`** | Production-ready reverse proxy primitives and byte buffer pooling. | `httputil.NewSingleHostReverseProxy`, `httputil.ReverseProxy`, `httputil.BufferPool`, `Director`, `ModifyResponse`, `ErrorHandler` |
| **`net/http/httptest`** | In-memory HTTP servers for unit and concurrent load tests. | `httptest.NewServer`, `httptest.Server` |
| **`net/url`** | Parsing and manipulating backend URLs. | `url.Parse`, `*url.URL` |
| **`net`** | Network addressing, IP parsing, and TCP dialer options. | `net.SplitHostPort`, `net.Dialer` |
| **`sync/atomic`** | Lock-free high-performance state manipulation (connection counts, health flags, dynamic balancer pointers). | `atomic.Bool`, `atomic.Int32`, `atomic.Uint64`, `atomic.Value` |
| **`sync`** | Goroutine coordination and memory pooling. | `sync.Mutex`, `sync.WaitGroup`, `sync.Pool` |
| **`context`** | Lifecycle management, cancellation, timeouts, and passing metadata per-request. | `context.Context`, `context.WithCancel`, `context.WithTimeout`, `context.WithValue` |
| **`os/signal` & `syscall`** | Trapping OS shutdown signals (`SIGINT`, `SIGTERM`) for graceful server shutdown. | `signal.NotifyContext` |
| **`time`** | Tickers, timeouts, parsing duration strings, request latency measurement. | `time.Duration`, `time.NewTicker`, `time.ParseDuration`, `time.Since` |
| **`hash/fnv`** | Fast non-cryptographic hashing for IP-hash load balancing. | `fnv.New32a`, `hash.Sum32` |
| **`log/slog`** | Modern structured logging (Go 1.21+). | `slog.NewTextHandler`, `slog.Info`, `slog.Warn`, `slog.Error` |
| **`encoding/json`** | Rendering JSON responses for status/health endpoints. | `json.NewEncoder` |

### Third-Party Dependencies
| Library | Purpose | Documentation |
| :--- | :--- | :--- |
| **`github.com/goccy/go-yaml`** | Fast and memory-efficient YAML parsing with full tag support. | Reads `configs/config.yaml` into Go structs. |

---

## 3. Project Directory Structure

```
Reverse-Proxy/
├── cmd/
│   └── proxy/
│       ├── main.go          # Server entry point, hot-reload, status routes, graceful shutdown
│       └── main_test.go     # End-to-end concurrency (100 users), failover, leastconn, and reload tests
├── configs/
│   └── config.yaml          # YAML configuration (backends, strategies, health check, timeouts, retry)
├── internal/
│   ├── backend/
│   │   └── backend.go       # Backend struct with lock-free atomic counters (healthy, conns, failures)
│   ├── balancer/
│   │   ├── balancer.go      # Balancer interface and factory constructor
│   │   ├── roundrobin.go    # Atomic lock-free Round-Robin algorithm
│   │   ├── leastconn.go     # Least Connections algorithm with round-robin tie breaking
│   │   └── iphash.go        # Consistent IP hashing algorithm (FNV-32a)
│   ├── config/
│   │   └── config.go        # YAML configuration unmarshaler with defaults & duration parser
│   ├── health/
│   │   ├── checker.go       # Active periodic health checker (concurrent HTTP probes)
│   │   └── passive.go       # Passive inline health checker (circuit-breaker trip on N failures)
│   ├── pool/
│   │   └── transport.go     # Tuned http.Transport (connection pool) & sync.Pool BufferPool (32KB)
│   └── proxy/
│       └── handler.go       # ReverseProxy builder, header injection, error handler, context tracking
├── pkg/
│   └── logger/
│       └── logger.go        # Structured slog logger & HTTP request logging middleware
├── go.mod
└── go.sum
```

---

## 4. Detailed Component Logic & Workflow

### 1. The Backend Model (`internal/backend/backend.go`)
Each upstream server is represented by a `Backend` struct.

#### Key Design Decision: Lock-Free Atomics
Instead of using `sync.Mutex` (which causes thread contention when 100+ concurrent requests hit the proxy), all metrics use atomic types from Go's `sync/atomic` package:
- `Healthy atomic.Bool`: `true` if server can receive traffic.
- `ActiveConns atomic.Int32`: Tracks concurrent in-flight requests currently being handled by this backend.
- `Failures atomic.Int32`: Consecutive failed attempts (used by passive health checking).
- `TotalRequests atomic.Uint64`: Cumulative counter for traffic metrics.
- `ReverseProxy *httputil.ReverseProxy`: Pre-allocated proxy instance tied directly to this upstream URL.

---

### 2. Load Balancing Strategies (`internal/balancer`)

All balancing strategies implement a common interface:
```go
type Balancer interface {
    NextBackend() *backend.Backend
    GetBackends() []*backend.Backend
}
```

#### A. Round-Robin (`roundrobin.go`)
- Uses `counter atomic.Uint64`.
- On every request: `idx = (counter.Add(1) - 1) % len(backends)`.
- If the selected backend is healthy, return it.
- If unhealthy, it continues up to `len(backends)` times to find the next healthy backend.

#### B. Least Connections (`leastconn.go`)
- Iterates over all healthy backends to find the one with `min(b.GetActiveConns())`.
- **Optimization 1 (Fast Exit)**: If it finds a backend with `0` active connections, it immediately returns it without scanning further.
- **Optimization 2 (Tie Breaking)**: Uses an atomic offset `lc.offset.Add(1)` as the loop starting point so ties between equally loaded servers are distributed in round-robin fashion.

#### C. IP Hash (`iphash.go`)
- Hashes client IP using FNV-32a (`hash/fnv`).
- `idx = (hashVal + i) % len(backends)`.
- Guarantees that requests from the same client IP always reach the same backend (session stickiness), while falling back gracefully to the next healthy backend if that upstream is down.

---

### 3. High-Performance Connection & Buffer Pooling (`internal/pool/transport.go`)

Proxying creates thousands of TCP sockets and copies millions of bytes. To prevent socket exhaustion and garbage collection pauses:

#### A. Custom `http.Transport` (Connection Reuse)
```go
&http.Transport{
    MaxIdleConns:          1000,             // Max idle connections across all hosts
    MaxIdleConnsPerHost:   100,              // Keep 100 persistent TCP connections open per backend
    MaxConnsPerHost:       500,              // Cap maximum sockets per host
    IdleConnTimeout:       90 * time.Second, // Keep TCP sockets alive for 90s
    DisableKeepAlives:     false,            // Enable HTTP Keep-Alive
    ForceAttemptHTTP2:     true,             // Enable HTTP/2 multiplexing where supported
}
```

#### B. Zero-Allocation `sync.Pool` Buffer Recycling (`BufferPool`)
By default, `httputil.ReverseProxy` allocates a new 32KB byte buffer for every proxied request to copy the payload. Under 1,000 req/sec, this creates 32 MB/s of garbage.
- We implement `httputil.BufferPool` using `sync.Pool`.
- Buffers of 32KB are borrowed on request start (`Get()`) and recycled on request completion (`Put()`).

---

### 4. Reverse Proxy Handler & Request Lifecycle (`internal/proxy/handler.go`)

The `ProxyHandler` implements `http.Handler` (`ServeHTTP(w, r)`):

```
Client HTTP Request
       │
       ▼
ProxyHandler.ServeHTTP()
       │
       ├─► 1. Load active Balancer from atomic.Value (Thread-Safe)
       │
       ├─► 2. Pick healthy backend via Balancer.NextBackend() (or NextBackendForIP)
       │
       ├─► 3. targetBackend.IncActiveConns() (defer DecActiveConns())
       │
       ├─► 4. Store *backend.Backend in r.Context()
       │
       └─► 5. targetBackend.ReverseProxy.ServeHTTP(w, reqWithContext)
                     │
                     ├─► Director: Sets Host, X-Forwarded-For, X-Real-IP, X-Forwarded-Proto
                     │
                     ├─► ModifyResponse: On HTTP success, calls PassiveChecker.RecordSuccess()
                     │
                     └─► ErrorHandler: On connection drop/refusal, calls PassiveChecker.RecordFailure()
```

---

### 5. Dual Health Checking System (`internal/health`)

#### A. Active Health Checker (`checker.go`)
- Background goroutine running on a configurable interval (default: `5s`).
- Uses `sync.WaitGroup` to probe all upstream backends **concurrently**.
- Performs an `HTTP GET` with a strict timeout (e.g. `2s`) against `/health`.
- If status is `200..399`, marks healthy; otherwise marks unhealthy and logs state transitions.

#### B. Passive Health Checker (`passive.go` - Circuit Breaker)
- Monitors live user traffic in real-time.
- Every time `ReverseProxy.ErrorHandler` triggers (e.g. backend crashes mid-request), `RecordFailure()` increments consecutive failure count.
- When failure count reaches `PassiveFailureThreshold` (default: `3`), the backend is **immediately tripped to unhealthy** so subsequent user requests stop hitting the broken backend before the active health checker even runs.
- A successful response resets the failure counter to `0`.

---

### 6. Atomic Hot Reload & Graceful Shutdown (`cmd/proxy/main.go`)

#### A. Zero-Downtime Atomic Hot Reload
- Route `POST /reload` triggers `reloadConfig()`.
- Reads `configs/config.yaml`.
- Constructs new backend list and new `Balancer`.
- Stops the old health checker goroutine via `context.CancelFunc` and starts the new one.
- Calls `proxyHandler.SetBalancer(newBalancer)` which performs an **`atomic.Value.Store()`**.
- In-flight requests continue seamlessly; new incoming requests immediately use the new balancer.

#### B. Clean Graceful Shutdown
- Captures `SIGINT` (Ctrl+C) and `SIGTERM` using `signal.NotifyContext(context.Background(), ...)`.
- When triggered, it stops active health check loops and invokes `srv.Shutdown(shutdownCtx)` with a 30-second drain window, letting all active requests complete cleanly before exiting.

---

## 5. Step-by-Step Blueprint: How to Rebuild from Scratch

If you are creating this project again from a blank directory, follow this order:

1. **Initialize Module & Dependencies**:
   ```bash
   go mod init high-performance-proxy
   go get github.com/goccy/go-yaml
   ```

2. **Step 1: Build the Backend Model (`internal/backend/backend.go`)**:
   - Define `Backend` struct with `atomic.Bool` and `atomic.Int32` counters.
   - Add thread-safe getter/setter helper methods.

3. **Step 2: Build the Balancer Interface & Algorithms (`internal/balancer/`)**:
   - Define `Balancer` interface.
   - Implement `RoundRobin` with `atomic.Uint64` modulo logic.
   - Implement `LeastConn` with minimum-connection loop and round-robin offset.
   - Implement `IPHash` using `hash/fnv`.

4. **Step 3: Build Resource Pooling (`internal/pool/transport.go`)**:
   - Create custom `http.Transport` with idle connection limits and keep-alive.
   - Create `BufferPool` with `sync.Pool` for 32KB byte buffers.

5. **Step 4: Build Health Checking (`internal/health/`)**:
   - Implement `HealthChecker` with ticker, `sync.WaitGroup`, and context cancellation.
   - Implement `PassiveChecker` with consecutive failure threshold and trip logic.

6. **Step 5: Build the Reverse Proxy Handler (`internal/proxy/handler.go`)**:
   - Wrap `httputil.NewSingleHostReverseProxy`.
   - Implement `Director` (forwarding headers), `ModifyResponse` (passive success), and `ErrorHandler` (passive failure).
   - Implement `ServeHTTP` using `atomic.Value` for hot-swapping the balancer.

7. **Step 6: Build Configuration Loader (`internal/config/config.go`)**:
   - Unmarshal YAML with default fallback values.
   - Parse `time.Duration` strings (`5s`, `2s`, `15s`).

8. **Step 7: Build Structured Logger & Access Middleware (`pkg/logger/logger.go`)**:
   - Configure `log/slog`.
   - Build `ResponseRecorder` to intercept HTTP status codes and response byte counts.

9. **Step 8: Wire Everything in `cmd/proxy/main.go`**:
   - Setup signal handling (`signal.NotifyContext`).
   - Register endpoints: `/`, `/status`, `/reload`, `/greet`.
   - Setup `http.Server` with graceful shutdown.

10. **Step 9: Test Thoroughly (`cmd/proxy/main_test.go`)**:
    - Use `httptest.NewServer` to test 1,000 concurrent users, 1 Lakh (100,000) requests across all servers, server crash failover, least-conn distribution, and hot-reload under load:
    ```bash
    # Run the 100,000 (1 Lakh) requests stress test
    go test -v -run TestProxy_100kRequests_AcrossAllServers ./cmd/proxy

    # Run the complete test suite (1000 users, failover, hot-reload, status)
    go test -v ./...

    # Run the 1,000 parallel worker throughput benchmark
    go test -bench=BenchmarkProxy_1000ConcurrentUsers -benchmem ./cmd/proxy
    ```

