# RunRPC

Booter, Loader, Executor, and Command Line shim for binary grpc services, as a grpc service.

Locally, this can serve as command line loader and generic sandbox agent for binaries, or as a kind of supervisor for other grpc services/subprocesses.

## The Dream

Ultimately, this is meant to be combined with https://github.com/accretional/plan92 and https://github.com/accretional/rpcfun to implement distributed execution of fully reflective functions across a multi-tenant, distributed, capability-based operating system based on grpc and proto

# Security

Yes, that's remote code execution.

We're also working on baking sandboxing into the operating system, but this doesn't necessarily have to run in a sandbox, and doesn't bake in assumptions about sandboxing by default.

You should only expose this service to a network if you are sandboxing it, and be careful running it as root locally too. This thing reads input and executes it. Only use it directly if you know what you're doing with that.

For your safety, in this repository we are only implementing and exposing versions of this code that act as a fork-exec loader.

## Usage

### Direct execution (Loader.Exec)

When called with arguments, runrpc executes the command using `execve()`, replacing itself with the command:

```bash
./runrpc echo "Hello World"
# Hello World

./runrpc ls -la
# (directory listing)

./runrpc cat main.go
# (file contents)
```

### Service introspection

When called without arguments from a terminal, runrpc lists available gRPC services:

```bash
./runrpc
# commander.Commander
# grpc.reflection.v1.ServerReflection
# grpc.reflection.v1alpha.ServerReflection
# loader.Loader
# runner.Runner
```

### Piping and filtering

You can pipe runrpc output to shell commands:

```bash
# Filter with grep
./runrpc | grep "Commander"
# commander.Commander

# Use with runrpc's exec mode
./runrpc | ./runrpc grep "reflection"
# grpc.reflection.v1.ServerReflection
# grpc.reflection.v1alpha.ServerReflection

# Limit output
./runrpc | head -3
```

### gRPC Server on stdin/stdout

When stdin is not a terminal (pipe/file) and has gRPC data:
- Creates an anonymous Unix socketpair
- gRPC server listens on one end
- Bridges stdin → socketpair and socketpair → stdout
- Serves until stdin closes

Special case for **PID 1**: Serves indefinitely (for container init scenarios)

## Services

### Loader
- `Exec(ExecutionArgs)` - Execute a binary with execve (replaces current process) ✓ Implemented
- `Link(stream BytesValue)` - Load binary data (like dlopen)
- `Load(LoadArgs)` - Execute via file descriptor (fexecve)

### Commander
- `Shell(Command)` - Execute shell commands and stream output (stdout/stderr) ✓ Implemented

### Runner
- `Fork(ForkRequest)` - Fork the current process
- `Spawn(SpawnRequest)` - POSIX spawn a new process
- `Stop(StopRequest)` - Stop a running process

## Building

```bash
# Install dependencies
go mod tidy

# Build runrpc
go build -o runrpc .

# Run tests
./test.sh
```

## How it Works

### Terminal Detection

runrpc detects whether stdin is:
- **Terminal (TTY)**: Shows service list and exits
- **Pipe/File**: Reads data and starts gRPC server
- **Closed immediately**: Shows service list and exits

### Anonymous Socketpair Architecture

When serving gRPC on stdin/stdout:

1. Creates `socketpair(AF_UNIX, SOCK_STREAM)` - two connected sockets
2. gRPC server listens on one end
3. stdin → clientConn (goroutine copies data)
4. clientConn → stdout (goroutine copies data)
5. Server runs until stdin closes

This design enables:
- Direct stdin/stdout gRPC transport
- No filesystem sockets required
- Clean process lifecycle management
- Natural piping with Unix tools
- Container-friendly (PID 1 handling)

## Examples

```bash
# Execute commands
./runrpc pwd
./runrpc echo "test"

# List services
./runrpc

# Filter services
./runrpc | grep Commander

# Pipe through multiple commands
./runrpc | head -3

# Use runrpc to filter runrpc output
./runrpc | ./runrpc grep "reflection"
```
