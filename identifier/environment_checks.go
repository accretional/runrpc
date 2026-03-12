package identifier

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// detectContainer returns true when the process should behave as if it is
// the init process inside a container.
//
//  1. os.Getpid() == 1
//  2. --pid1 flag supplied on the command line
//  3. /proc/1/cgroup contains typical container control-group paths
func detectContainer() bool {
	if os.Getpid() == 1 {
		return true
	}
	if *flagPID1 {
		return true
	}
	return cgroupLooksLikeContainer()
}

// cgroupLooksLikeContainer reads /proc/1/cgroup (Linux only) and returns
// true when any line contains a path segment that implies a container
// runtime.
func cgroupLooksLikeContainer() bool {
	f, err := os.Open("/proc/1/cgroup")
	if err != nil {
		return false
	}
	defer f.Close()

	containerHints := []string{
		"docker",
		"kubepods",
		"containerd",
		"lxc",
		"/ecs/",
		"garden",
		"actions_job",
		"crio",
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.ToLower(scanner.Text())

		for _, hint := range containerHints {
			if strings.Contains(line, hint) {
				return true
			}
		}

		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 {
			path := parts[2]
			if path != "/" && path != "" {
				if strings.Contains(path, ".scope") || strings.Contains(path, ".slice") {
					return true
				}
			}
		}
	}

	return false
}

// verifyDomain checks DNS resolution, TLS validity, and HTTP reachability
// for a domain.
func verifyDomain(domain string) error {
	addrs, err := net.LookupHost(domain)
	if err != nil {
		return fmt.Errorf("dns lookup: %w", err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("dns lookup returned zero addresses")
	}

	conn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: 10 * time.Second},
		"tcp",
		net.JoinHostPort(domain, "443"),
		&tls.Config{
			ServerName: domain,
			MinVersion: tls.VersionTLS12,
		},
	)
	if err != nil {
		return fmt.Errorf("tls dial: %w", err)
	}
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return fmt.Errorf("peer presented no certificates")
	}

	leaf := certs[0]
	now := time.Now()
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return fmt.Errorf("leaf certificate not valid at current time (notBefore=%s notAfter=%s)",
			leaf.NotBefore, leaf.NotAfter)
	}

	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	opts := x509.VerifyOptions{
		DNSName:       domain,
		Intermediates: intermediates,
	}
	if _, err := leaf.Verify(opts); err != nil {
		return fmt.Errorf("certificate chain verification: %w", err)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://" + domain)
	if err != nil {
		return fmt.Errorf("https GET: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode >= 500 {
		return fmt.Errorf("https GET returned server error: %d", resp.StatusCode)
	}

	return nil
}

// IsPID1 exposes the container/pid1 detection result.
func IsPID1() bool { return isContainerised }

// MustParseContainerID tries to extract a 64-char hex container ID from
// /proc/1/cgroup (useful for logging).
func MustParseContainerID() string {
	f, err := os.Open("/proc/1/cgroup")
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		idx := strings.LastIndex(line, "/")
		if idx == -1 {
			continue
		}
		id := line[idx+1:]
		if len(id) == 64 && isHex(id) {
			return id
		}
		if strings.HasPrefix(id, "docker-") {
			id = strings.TrimPrefix(id, "docker-")
			id = strings.TrimSuffix(id, ".scope")
			if len(id) == 64 && isHex(id) {
				return id
			}
		}
	}
	return ""
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// runEnvironmentChecks runs container detection and domain verification.
func runEnvironmentChecks() {
	isContainerised = detectContainer()
	log.Printf("[identifier] container=%v authority=%s", isContainerised, rootAuthority)

	if err := verifyDomain(rootAuthority); err != nil {
		log.Printf("[identifier] authority verification FAILED for %s: %v", rootAuthority, err)
		return
	}
	log.Printf("[identifier] authority %s verified (reachable, valid TLS)", rootAuthority)
}
