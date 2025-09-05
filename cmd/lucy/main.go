package main

import (
	"context"
	"fmt"
	"os"

	"github.com/steelcityamir/lucy/internal/config"
	"github.com/steelcityamir/lucy/internal/proxy"
)

func main() {
	cfg := config.ParseFlags()
	p := proxy.NewProxyServer(cfg)

	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
