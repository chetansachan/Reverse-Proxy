package proxy

import "net/http/httputil"

type ProxyHandler struct {
	ReverseProxy *httputil.ReverseProxy
}