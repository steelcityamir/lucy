package proxy

import (
	"fmt"
	"time"
)

// timestamp returns a readable date+time string
func timestamp() string {
	return time.Now().Format("2006-01-02 15:04:05.000")
}

// PrettyLogRequest prints a human-readable request log
func PrettyLogRequest(method, url string, headers map[string]string, body string) {
	fmt.Printf("[%s] ➡️ %s %s\n", timestamp(), method, url)
	for k, v := range headers {
		fmt.Printf("   %s: %s\n", k, v)
	}
	if len(body) > 0 {
		fmt.Printf("   Body: %s\n", body)
	}
}

// PrettyLogResponse prints a human-readable response log
func PrettyLogResponse(status int, url string, headers map[string]string, body string, duration time.Duration) {
	fmt.Printf("\n[%s] ⬅️ %d %s (%v)\n", timestamp(), status, url, duration)
	for k, v := range headers {
		fmt.Printf("   %s: %s\n", k, v)
	}
	if len(body) > 0 {
		fmt.Printf("   Response: %s\n", body)
	}
	fmt.Println("---")
}
