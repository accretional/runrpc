# runrpc

A single binary that serves four roles depending on how it's invoked:

1. **Loader** — Start a bidirectional stream and Load into something else
   (`Loader(Something) -> Something`)

2. **Client** — Connect to a remote instance of itself and
   authenticate/interact with it

3. **Shell tool** — Run commands that involve stream processing and
   interaction with remote instances, driven by something working in
   the loop (a human on a terminal, a script, another process)

4. **Server / init** — Be a generic gRPC server or container init
   process

## Boot sequence (server side)

```
Loader() -> Identifier() -> branch on environment:

  pid1, out of container:
    Continue booting into a "controller" with many more services enabled

  pid1, in container:
    Wait for gRPC authentication from cli/api client
    May Load/Run something, or become a "controller"

  not pid1, out of container:
    Wait for direct authentication with caller via login/terminal
    Might just be someone checking options or running something locally
    Don't assume they want the full stack

  not pid1, in container:
    Probably just shut down
```

## Service pipeline

```
Loader(Something) -> Something
Commander(Command) -> Commander
Run(Execution) -> Run + Execution -> Run -> stop
```

## Authentication model

The Identifier service has two concepts:

- **Login providers** (0 or more) — client-initiated, run during boot,
  block the gRPC server from starting. Configurable per environment.

- **Auth flows** (0 or more) — server-involved, handle the Authenticate
  RPC after boot. Can include login-capable flows (server tells client
  how to authenticate: "go do X, come back with the result").

Both are configured externally. The identifier package defines the
interfaces (`LoginProvider`, `AuthFlow`) and runs whatever it's given.
It knows nothing about any specific authentication implementation.

Providers are things like:
- `auth/otp` — single token, printed at boot or passed via CLI
- `auth/workos` — WorkOS magic auth, invitations, device auth, JWT
- (future) anything else that implements the interfaces

## What's here now

- `identifier/` — gRPC Identifier service, provider-agnostic interfaces
- `identifier/authflows/workos/` — WorkOS auth flow implementations
- `auth/otp/` — simple OTP provider
- `auth/workos/` — WorkOS API client
- `cli/` — client binary (decoupled from server-side auth)
- `loader/`, `commander/`, `runner/` — other gRPC services (not yet
  wired into the full boot sequence above)
- `filer/` — proto definitions for shared types
