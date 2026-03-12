package identifier

import (
	"flag"
	"log"
	"os"
	"sync"

	"github.com/accretional/runrpc/filer"
)

// CLI flags.
var (
	flagPID1          = flag.Bool("pid1", false, "Force PID-1 / init behaviour even when os.Getpid() != 1")
	flagRootAuthority = flag.String("root_authority", "accretional.com", "Root authority domain")
	flagRootIdentity  = flag.String("root_identity", "fred", "Root identity username")
	flagWorkOSAPIKey  = flag.String("workos_api_key", "", "WorkOS API key (overrides WORKOS_API_KEY env)")
	flagWorkOSClient  = flag.String("workos_client_id", "", "WorkOS client ID (overrides WORKOS_CLIENT_ID env)")
)

// Package-level state.
var (
	rootAuthority string
	rootIdentity  string

	isContainerised bool

	// tokens is a concurrent map[string]*filer.Resource.
	tokens sync.Map

	// loginProviders are run during Init() before the gRPC server starts.
	loginProviders   []LoginProvider
	loginProvidersMu sync.Mutex
)

// RegisterLoginProvider adds a login provider that will run during Init().
func RegisterLoginProvider(p LoginProvider) {
	loginProvidersMu.Lock()
	loginProviders = append(loginProviders, p)
	loginProvidersMu.Unlock()
}

// Init is the single public entry point. Call it from main() after
// flag.Parse(). It runs environment checks, pushes CLI flags into env
// vars, then blocks on any registered login providers.
func Init() {
	if !flag.Parsed() {
		flag.Parse()
	}

	// Set package state from flags.
	rootAuthority = *flagRootAuthority
	rootIdentity = *flagRootIdentity

	// Push WorkOS flags into env for downstream packages.
	if v := *flagWorkOSAPIKey; v != "" {
		os.Setenv("WORKOS_API_KEY", v)
	}
	if v := *flagWorkOSClient; v != "" {
		os.Setenv("WORKOS_CLIENT_ID", v)
	}

	// Environment checks (container detection, domain verification).
	runEnvironmentChecks()

	// Run login providers (blocks until all complete or first succeeds).
	runLoginProviders()

	log.Printf("[identifier] boot complete (authority=%s identity=%s container=%v)",
		rootAuthority, rootIdentity, isContainerised)
}

// runLoginProviders executes registered login providers sequentially.
func runLoginProviders() {
	loginProvidersMu.Lock()
	providers := make([]LoginProvider, len(loginProviders))
	copy(providers, loginProviders)
	loginProvidersMu.Unlock()

	if len(providers) == 0 {
		return
	}

	for _, p := range providers {
		log.Printf("[identifier/login] running provider %q", p.Name())
		res, err := p.Login()
		if err != nil {
			log.Printf("[identifier/login] provider %q failed: %v", p.Name(), err)
			continue
		}
		if res != nil {
			log.Printf("[identifier/login] provider %q authenticated: %s", p.Name(), res.GetName())
			StoreToken(p.Name(), res)
		}
	}
}

// Public accessors.

// RootAuthority returns the resolved root authority domain.
func RootAuthority() string { return rootAuthority }

// RootIdentity returns the resolved root identity name.
func RootIdentity() string { return rootIdentity }

// FormatRootEmail returns root_identity@root_authority.
func FormatRootEmail() string {
	return rootIdentity + "@" + rootAuthority
}

// StoreToken writes a Resource into the token map under the given key.
func StoreToken(key string, r *filer.Resource) {
	tokens.Store(key, r)
}

// LoadToken retrieves a Resource from the token map.
func LoadToken(key string) (*filer.Resource, bool) {
	v, ok := tokens.Load(key)
	if !ok {
		return nil, false
	}
	return v.(*filer.Resource), true
}
