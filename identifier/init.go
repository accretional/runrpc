// github.com/accretional/runrpc/identifier/init.go
package identifier

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	workos "github.com/workos/workos-go/v4/pkg"
	"github.com/workos/workos-go/v4/pkg/magicauth"

	resourcepb "github.com/accretional/runrpc/filer"
)

// ---------------------------------------------------------------------------
// CLI flags
// ---------------------------------------------------------------------------

var (
	flagPID1          = flag.Bool("pid1", false, "Force PID-1 / init behaviour even when os.Getpid() != 1")
	flagRootAuthority = flag.String("root_authority", "accretional.com", "Root authority domain")
	flagRootIdentity  = flag.String("root_identity", "fred", "Root identity username")
)

// ---------------------------------------------------------------------------
// Package-level state
// ---------------------------------------------------------------------------

var (
	rootAuthority string
	rootIdentity  string

	systemSecret string
	rootOTP      string

	// tokens is a concurrent map[string]*resourcepb.Resource.
	// Written once per key then read at high throughput, so sync.Map is ideal.
	tokens sync.Map

	// secrets is an append-only slice guarded by a mutex so it can be
	// extended safely from any goroutine.
	secrets   []string
	secretsMu sync.RWMutex

	isContainerised bool
)

// ---------------------------------------------------------------------------
// Public accessors
// ---------------------------------------------------------------------------

// StoreToken writes a Resource into the token map under the given key.
func StoreToken(key string, r *resourcepb.Resource) {
	tokens.Store(key, r)
}

// LoadToken retrieves a Resource from the token map.  The second return
// value is false when the key is absent.
func LoadToken(key string) (*resourcepb.Resource, bool) {
	v, ok := tokens.Load(key)
	if !ok {
		return nil, false
	}
	return v.(*resourcepb.Resource), true
}

// AppendSecret adds a secret string to the secrets slice.
func AppendSecret(s string) {
	secretsMu.Lock()
	secrets = append(secrets, s)
	secretsMu.Unlock()
}

// Secrets returns a snapshot copy of the current secrets slice.
func Secrets() []string {
	secretsMu.RLock()
	defer secretsMu.RUnlock()
	dst := make([]string, len(secrets))
	copy(dst, secrets)
	return dst
}

// ---------------------------------------------------------------------------
// Initialisation stages – called from Init()
// ---------------------------------------------------------------------------

// Init is the single public entry point.  Call it from main() after
// flag.Parse() so that all CLI flags are available.
func Init() {
	if !flag.Parsed() {
		flag.Parse()
	}

	init1()
	init2()
	init3()
	init4()
}

// init1 – identity bootstrap

func init1() {
	// ---- PID-1 / container detection ----
	isContainerised = detectContainer()

	// ---- authority & identity ----
	rootAuthority = *flagRootAuthority
	rootIdentity = *flagRootIdentity

	// ---- generate secrets ----
	systemSecret = uuid.NewString()
	rootOTP = uuid.NewString()

	// ---- seed the secrets slice ----
	secrets = []string{systemSecret, rootOTP}

	log.Printf("[init1] root_authority=%s root_identity=%s container=%v",
		rootAuthority, rootIdentity, isContainerised)
}

// init2 – conditional OTP disclosure

func init2() {
	if isContainerised {
		// Print to stdout so orchestration tooling (docker logs, k8s) can
		// capture it on first boot.
		fmt.Printf("[init2] root_otp=%s\n", rootOTP)
	}
}

// init3 – WorkOS Magic Auth
func init3() {
	apiKey := os.Getenv("WORKOS_API_KEY")
	if apiKey == "" {
		log.Println("[init3] WORKOS_API_KEY not set – skipping magic auth enrolment")
		return
	}

	workos.SetAPIKey(apiKey)

	email := fmt.Sprintf("%s@%s", rootIdentity, rootAuthority)

	sess, err := magicauth.CreateSession(magicauth.CreateSessionOpts{
		Email: email,
	})
	if err != nil {
		log.Printf("[init3] magic auth session for %s failed: %v", email, err)
		return
	}

	log.Printf("[init3] magic auth session created for %s (id=%s)", email, sess.ID)
}

// init4 – domain reachability & TLS verification

func init4() {
	if err := verifyDomain(rootAuthority); err != nil {
		log.Printf("[init4] root_authority verification FAILED for %s: %v",
			rootAuthority, err)
		return
	}
	log.Printf("[init4] root_authority %s verified (reachable, valid TLS)", rootAuthority)
}

func verifyDomain(domain string) error {
	// 1. DNS resolution
	addrs, err := net.LookupHost(domain)
	if err != nil {
		return fmt.Errorf("dns lookup: %w", err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("dns lookup returned zero addresses")
	}

	// 2. TLS handshake – let crypto/tls verify the chain against the
	//    system root pool.
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

	// 3. Walk the peer certificates and report any issues.
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

	// Verify the chain explicitly so we surface intermediate problems.
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

	// 4. HTTP-level reachability (follows redirects, honours timeouts).
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

// TODO: fix name slightly, we want to call true pid host and container pid container
// detectContainer returns true when the process should behave as if it is
// the init process inside a container.  The check is intentionally broad:
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

// TODO: make this less sloppy
// cgroupLooksLikeContainer reads /proc/1/cgroup (Linux only) and returns
// true when any line contains a path segment that implies a container
// runtime (docker, kubepods, containerd, lxc, etc.) or the unified
// cgroup hierarchy shows a non-root slice.
func cgroupLooksLikeContainer() bool {
	f, err := os.Open("/proc/1/cgroup")
	if err != nil {
		// Not on Linux or no access – assume bare metal.
		return false
	}
	defer f.Close()

	containerHints := []string{
		"docker",
		"kubepods",
		"containerd",
		"lxc",
		"/ecs/",
		"garden",       // Cloud Foundry
		"actions_job",  // GitHub Actions
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

		// cgroupv2 unified hierarchy: a non-root slice (e.g. 0::/<slice>)
		// also implies containerisation.
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 {
			path := parts[2]
			if path != "/" && path != "" {
				// e.g. "0::/system.slice/docker-abc123.scope"
				if strings.Contains(path, ".scope") || strings.Contains(path, ".slice") {
					return true
				}
			}
		}
	}

	return false
}

// IsPID1 exposes the container/pid1 detection result.
func IsPID1() bool { return isContainerised }

// RootAuthority returns the resolved root authority domain.
func RootAuthority() string { return rootAuthority }

// RootIdentity returns the resolved root identity name.
func RootIdentity() string { return rootIdentity }

// SystemSecret returns the generated system secret UUID.
func SystemSecret() string { return systemSecret }

// RootOTP returns the generated root one-time-password UUID.
func RootOTP() string { return rootOTP }

// FormatRootEmail returns root_identity@root_authority.
func FormatRootEmail() string {
	return rootIdentity + "@" + rootAuthority
}

// MustParseContainerID is a small helper that tries to extract a 64-char
// hex container ID from /proc/1/cgroup (useful for logging).
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
		// Docker / containerd IDs are 64 hex chars.
		if len(id) == 64 && isHex(id) {
			return id
		}
		// Might be prefixed, e.g. docker-<id>.scope
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

// Ensure flag defaults survive repeated tests that call flag.Parse().
func init() {
	// Touch the flags so that `go vet` doesn't complain about unused
	// imports when this package is vendored but Init() hasn't been
	// called yet.
	_ = strconv.Itoa(0)
}
