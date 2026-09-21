package automation

import (
	"bufio"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"taskboard/internal/domain"
	"time"
)

type allowedEndpoint struct {
	host string
	port string
}

func (a allowedEndpoint) key() string {
	return net.JoinHostPort(strings.ToLower(strings.TrimSpace(a.host)), strings.TrimSpace(a.port))
}

// modelAPIAllowlist is the only network the CLI process may reach when the
// selected profile has NetworkMode=none. Tool and project code stay in an
// unrouted namespace; the host-side CONNECT proxy then permits these
// destinations and refuses everything else.
func modelAPIAllowlist(provider domain.ProviderSetting) []allowedEndpoint {
	seen := map[string]allowedEndpoint{}
	add := func(host, port string) {
		host = strings.ToLower(strings.TrimSpace(host))
		host = strings.TrimSuffix(host, ".")
		port = strings.TrimSpace(port)
		if host == "" || port == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
			return
		}
		if strings.ContainsAny(host, "/\\") || strings.Contains(host, "..") {
			return
		}
		item := allowedEndpoint{host: host, port: port}
		seen[item.key()] = item
	}
	add("api.openai.com", "443")
	add("chatgpt.com", "443")
	add("ab.chatgpt.com", "443")
	add("api.anthropic.com", "443")
	add("api.x.ai", "443")
	if base, err := url.Parse(strings.TrimSpace(provider.BaseURL)); err == nil && base.Host != "" {
		port := base.Port()
		if port == "" {
			if strings.EqualFold(base.Scheme, "http") {
				port = "80"
			} else {
				port = "443"
			}
		}
		add(base.Hostname(), port)
	}
	addr := strings.TrimSpace(os.Getenv("TASKBOARD_ADDR"))
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	if host, port, err := net.SplitHostPort(addr); err == nil {
		switch host {
		case "0.0.0.0", "::", "[::]":
			add("127.0.0.1", port)
			add("localhost", port)
			add("::1", port)
		default:
			add(host, port)
			if host == "127.0.0.1" {
				add("localhost", port)
				add("::1", port)
			}
		}
	}
	result := make([]allowedEndpoint, 0, len(seen))
	for _, item := range seen {
		result = append(result, item)
	}
	return result
}

func allowlistSet(items []allowedEndpoint) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		set[item.key()] = struct{}{}
		if item.host == "::1" {
			set[net.JoinHostPort("::1", item.port)] = struct{}{}
		}
	}
	return set
}

type modelAPIProxy struct {
	path     string
	listener net.Listener
	allow    map[string]struct{}
	stopOnce sync.Once
}

func startModelAPIProxy(allow []allowedEndpoint) (*modelAPIProxy, error) {
	f, err := os.CreateTemp("", "shipyard-model-api-*.sock")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	_ = f.Close()
	_ = os.Remove(path)
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, err
	}
	proxy := &modelAPIProxy{path: path, listener: listener, allow: allowlistSet(allow)}
	go proxy.serve()
	return proxy, nil
}

func (p *modelAPIProxy) serve() {
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			return
		}
		go handleModelAPIConnect(conn, p.allow)
	}
}

func (p *modelAPIProxy) Close() error {
	var err error
	p.stopOnce.Do(func() {
		err = p.listener.Close()
		_ = os.Remove(p.path)
	})
	return err
}

func handleModelAPIConnect(conn net.Conn, allow map[string]struct{}) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	method, target, _, ok := parseConnectLine(line)
	if !ok || !strings.EqualFold(method, "CONNECT") {
		_, _ = io.WriteString(conn, "HTTP/1.1 405 Method Not Allowed\r\nConnection: close\r\n\r\n")
		return
	}
	if err := discardHTTPHeaders(reader); err != nil {
		return
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		_, _ = io.WriteString(conn, "HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n")
		return
	}
	key := net.JoinHostPort(strings.ToLower(strings.TrimSpace(host)), strings.TrimSpace(port))
	if _, ok := allow[key]; !ok {
		_, _ = io.WriteString(conn, "HTTP/1.1 403 Forbidden\r\nConnection: close\r\n\r\n")
		return
	}
	upstream, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 15*time.Second)
	if err != nil {
		_, _ = io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n")
		return
	}
	defer upstream.Close()
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	_ = upstream.SetDeadline(time.Time{})
	go func() { _, _ = io.Copy(upstream, reader) }()
	_, _ = io.Copy(conn, upstream)
}

func parseConnectLine(line string) (method, target, version string, ok bool) {
	line = strings.TrimSpace(line)
	parts := strings.Split(line, " ")
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

func discardHTTPHeaders(reader *bufio.Reader) error {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if line == "\r\n" || line == "\n" {
			return nil
		}
	}
}
