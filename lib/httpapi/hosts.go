package httpapi

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode"
)

// Validate allowed hosts don't contain whitespace, commas, schemes, or ports.
// Viper/Cobra use different separators (space for env vars, comma for flags),
// so these characters likely indicate user error.
func parseAllowedHosts(input []string) ([]string, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("the list must not be empty")
	}
	if slices.Contains(input, "*") {
		return []string{"*"}, nil
	}
	// First pass: whitespace & comma checks (surface these errors first)
	// Viper/Cobra use different separators (space for env vars, comma for flags),
	// so these characters likely indicate user error.
	for _, item := range input {
		for _, r := range item {
			if unicode.IsSpace(r) {
				return nil, fmt.Errorf("'%s' contains whitespace characters, which are not allowed", item)
			}
		}
		if strings.Contains(item, ",") {
			return nil, fmt.Errorf("'%s' contains comma characters, which are not allowed", item)
		}
	}
	// Second pass: scheme check
	for _, item := range input {
		if strings.Contains(item, "http://") || strings.Contains(item, "https://") {
			return nil, fmt.Errorf("'%s' must not include http:// or https://", item)
		}
	}
	hosts := make([]*url.URL, 0, len(input))
	// Third pass: url parse
	for _, item := range input {
		trimmed := strings.TrimSpace(item)
		u, err := url.Parse("http://" + trimmed)
		if err != nil {
			return nil, fmt.Errorf("'%s' is not a valid host: %w", item, err)
		}
		hosts = append(hosts, u)
	}
	// Fourth pass: port check
	for _, u := range hosts {
		if u.Port() != "" {
			return nil, fmt.Errorf("'%s' must not include a port", u.Host)
		}
	}
	hostStrings := make([]string, 0, len(hosts))
	for _, u := range hosts {
		hostStrings = append(hostStrings, u.Hostname())
	}
	return hostStrings, nil
}

// Validate allowed origins
func parseAllowedOrigins(input []string) ([]string, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("the list must not be empty")
	}
	if slices.Contains(input, "*") {
		return []string{"*"}, nil
	}
	// Viper/Cobra use different separators (space for env vars, comma for flags),
	// so these characters likely indicate user error.
	for _, item := range input {
		for _, r := range item {
			if unicode.IsSpace(r) {
				return nil, fmt.Errorf("'%s' contains whitespace characters, which are not allowed", item)
			}
		}
		if strings.Contains(item, ",") {
			return nil, fmt.Errorf("'%s' contains comma characters, which are not allowed", item)
		}
	}
	origins := make([]string, 0, len(input))
	for _, item := range input {
		trimmed := strings.TrimSpace(item)
		u, err := url.Parse(trimmed)
		if err != nil {
			return nil, fmt.Errorf("'%s' is not a valid origin: %w", item, err)
		}
		origins = append(origins, fmt.Sprintf("%s://%s", u.Scheme, u.Host))
	}
	return origins, nil
}

// ParseBindAddress validates the address to listen on: an IP address or a
// hostname, without a port. IPv6 addresses may be bracketed ("[::1]"); the
// brackets are removed. Empty means all interfaces.
func ParseBindAddress(input string) (string, error) {
	addr := strings.TrimSpace(input)
	if addr == "" {
		return "", nil
	}
	if strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]") {
		addr = addr[1 : len(addr)-1]
		if net.ParseIP(addr) == nil {
			return "", fmt.Errorf("'%s' is not a valid IPv6 address", input)
		}
		return addr, nil
	}
	if net.ParseIP(addr) != nil {
		return addr, nil
	}
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return "", fmt.Errorf("'%s' must not include a port; use --port", input)
	}
	if !isValidHostname(addr) {
		return "", fmt.Errorf("'%s' is not a valid IP address or hostname", input)
	}
	return addr, nil
}

// isValidHostname reports whether name is a DNS hostname (RFC 1123 labels).
func isValidHostname(name string) bool {
	name = strings.TrimSuffix(name, ".")
	if name == "" || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// hostAuthorizationMiddleware enforces that the request Host header matches one of the allowed
// hosts, ignoring any port in the comparison. If allowedHosts is empty, all hosts are allowed.
// Always uses url.Parse("http://" + r.Host) to robustly extract the hostname (handles IPv6).
func hostAuthorizationMiddleware(allowedHosts []string, badHostHandler http.Handler) func(next http.Handler) http.Handler {
	// Copy for safety; also build a map for O(1) lookups with case-insensitive keys.
	allowed := make(map[string]struct{}, len(allowedHosts))
	for _, h := range allowedHosts {
		allowed[strings.ToLower(h)] = struct{}{}
	}
	wildcard := slices.Contains(allowedHosts, "*")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if wildcard { // wildcard semantics: allow all
				next.ServeHTTP(w, r)
				return
			}
			// Extract hostname from the Host header using url.Parse; ignore any port.
			hostHeader := r.Host
			if hostHeader == "" {
				badHostHandler.ServeHTTP(w, r)
				return
			}
			if u, err := url.Parse("http://" + hostHeader); err == nil {
				hostname := u.Hostname()
				if _, ok := allowed[strings.ToLower(hostname)]; ok {
					next.ServeHTTP(w, r)
					return
				}
			}
			badHostHandler.ServeHTTP(w, r)
		})
	}
}
