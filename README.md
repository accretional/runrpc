# RunRPC

Booter, Loader, Executor, and Command Line shim for binary grpc services, as a grpc service. 

Locally, this can serve as command line loader and generic sandbox agent for binaries, or as a kind of supervisor for other grpc services/subprocesses.

## The Dream

Ulimtately, this is meant to be combined with https://github.com/accretional/plan92 and https://github.com/accretional/rpcfun to implement distributed execution of fully reflective functions across a multi-tenant, distributed, capability-based operating system based on grpc and proto

# Security

Yes, that's remote code execution.

We're also working on baking sandboxing into the operating system, but this doesn't necessarily have to run in a sandbox, and doesn't bake in assumptions about sandboxing by default. 

You should only expose this service to a network if you are sandboxing it, and be careful running it as root locally too. This thing reads input and executes it. Only use it directly if you know what you're doing with that.

For your safety, in this repository we are only implementing and exposing versions of this code that act as a fork-exec loader.
