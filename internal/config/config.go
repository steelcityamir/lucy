package config

import (
	"flag"
	"time"
)

type Config struct {
	Port           int
	RequestTimeout time.Duration
	ServerTimeout  time.Duration
	MaxBodySize    int64
}

func ParseFlags() Config {
	port := flag.Int("port", 8080, "Port to listen on")
	requestTimeout := flag.Duration("timeout", 30*time.Second, "Request timeout")
	serverTimeout := flag.Duration("server-timeout", 30*time.Second, "Server timeout")
	maxBodySize := flag.Int64("max-body-size", 10*1024*1024, "Maximum body size in bytes")
	flag.Parse()

	return Config{
		Port:           *port,
		RequestTimeout: *requestTimeout,
		ServerTimeout:  *serverTimeout,
		MaxBodySize:    *maxBodySize,
	}
}
