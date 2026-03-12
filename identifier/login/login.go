// Package login provides boot-time login orchestration for the
// identifier service. Login providers are client-initiated and run
// before the gRPC server starts.
package login

import (
	"log"

	"github.com/accretional/runrpc/filer"
	"github.com/accretional/runrpc/identifier"
)

// Run executes login providers sequentially, returning the first
// successful result. Returns nil if no providers are given or all fail.
func Run(providers []identifier.LoginProvider) *filer.Resource {
	for _, p := range providers {
		log.Printf("[login] running provider %q", p.Name())
		res, err := p.Login()
		if err != nil {
			log.Printf("[login] provider %q failed: %v", p.Name(), err)
			continue
		}
		if res != nil {
			log.Printf("[login] provider %q authenticated: %s", p.Name(), res.GetName())
			return res
		}
	}
	return nil
}
