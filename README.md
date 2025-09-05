# lucy
Lucy is a lightweight HTTP debugging proxy written in Go

A lightweight HTTP debugging proxy for developers. See exactly what your applications are sending and receiving over the network with zero code changes.

## Features

- 📤 **HTTP Request Logging** - Method, URL, headers, and body
- 📥 **Response Monitoring** - Status, headers, body, and timing
- 🔒 **HTTPS Tunneling** - Transparent HTTPS support with connection logging
- ⚡ **Fast & Lightweight** - Built in Go with excellent concurrency
- 🎯 **Zero Configuration** - Works with any HTTP client via proxy settings
- 📊 **Pretty JSON Formatting** - Automatic formatting of JSON payloads
- ⏱️ **Performance Timing** - See response times for every request
- 🛡️ **Graceful Shutdown** - Clean exit with Ctrl+C

## Use Cases

- **🐛 Debug API Integration Issues** - See exactly what your app is sending
- **🔍 Discover Hidden Dependencies** - Find out what services your app calls
- **⚡ Performance Analysis** - Identify slow API calls
- **🔐 Authentication Debugging** - Verify headers and tokens are correct
- **📊 API Usage Monitoring** - Track which endpoints are being used
- **🧪 Development & Testing** - Monitor requests during development

## How It Works

Lucy acts as an HTTP proxy between your application and the internet:

```
Your App → Lucy → Internet
```

## Quick Start

```bash
# Clone and build
git clone https://github.com/steelcityamir/lucy
cd lucy
go build -o lucy ./cmd/lucy

# Start the proxy (default port is 8080)
./lucy

# Make request to the proxy
curl -x http://localhost:8080 http://api.github.com/zen
```

## Installation

### From Source
```bash
git clone https://github.com/steelcityamir/lucy
cd lucy
go install
```

### Using Go Install
```bash
go install github.com/steelcityamir/lucy@latest
```

### Download Binary
Download the latest release from [GitHub Releases](https://github.com/steelcityamir/lucy/releases).

## Usage

### Basic Usage
```bash
$ ./lucy
🚀 Lucy started on port 8080 (Ctrl-C to stop)
👀 Watching for requests...
```

## Send an HTTP request to the proxy
```bash
curl -x http://localhost:8080 http://api.github.com/zen
```

## Output
```
[2025-09-05 12:06:37.737] ➡️ GET http://api.github.com/zen
   User-Agent: curl/8.7.1
   Accept: */*

[2025-09-05 12:06:37.875] ⬅️ 200 OK http://api.github.com/zen (138.242833ms)
   Content-Type: text/plain;charset=utf-8
   Content-Length: 15
   Response: Encourage flow.
```

## Configuration

### Command Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | 8080 | Port to listen on |
| `--timeout` | 30s | Request timeout |
| `--server-timeout` | 30s | Server read/write timeout |
| `--max-body-size` | 10MB | Maximum request/response body size |

### Environment Variables
You can also configure using environment variables:
```bash
export LUCY_PORT=8080
export LUCY_TIMEOUT=30s
```


### HTTP Requests
For HTTP traffic, Lucy can see and log:
- Request method, URL, and headers
- Request body (with pretty JSON formatting)
- Response status, headers, and body
- Response timing

### HTTPS Requests
For HTTPS traffic, Lucy creates secure tunnels and logs:
- Connection establishment to target hosts
- Connection duration and timing

> [!NOTE]
> HTTPS request/response content is encrypted and cannot be logged by Lucy.

## FAQ

### Why can't I see HTTPS request details?

HTTPS traffic is encrypted end-to-end for security. Lucy can only see connection metadata (which hosts, timing) but not the actual request/response content. This is by design and maintains security.

If you need to debug HTTPS content, consider:
- Using HTTP for internal services during development
- Tools like mitmproxy with certificate installation
- Application-level logging

### Does this work with all HTTP clients?

Yes! Any HTTP client that supports proxy configuration will work:
- cURL, wget, httpie
- Browsers (Chrome, Firefox, etc.)
- Programming languages (Python, Node.js, Go, Java, etc.)
- Mobile apps and desktop applications

### Is this safe to use in production?

Lucy is designed for development and debugging. While it handles traffic safely, running proxy servers in production requires careful security considerations. Use appropriate firewall rules and access controls if deploying in shared environments.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) for details.

## Support

For support, please open an issue in the GitHub issue tracker for this project.