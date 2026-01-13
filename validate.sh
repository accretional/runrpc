#!/bin/bash

set -e

# Case 0: nothing on stdin but args, interpret as Commander.Command
# ./runrpc command arg1 arg2 arg3 -> command arg1 arg2 arg3
./runrpc echo "hi"
# hi

# No args, there are three cases
# Case 1: pid1 or stdin is a socket, start and serve on the stdin fd after swapping/copying it to a socket connection
./runrpc
# continues indefinitely

# Case 2: not pid1, stdin is not a socket, stdin closed without data
# We are first command on a subshell, should call reflection api on ourselves and dump readable textprotos
./runrpc
# commander.Commander                     
#     grpc.reflection.v1.ServerReflection
#     etc.

# Case 3: not pid1, stdin got data
# We are being streamed data on a subshell, should process input accordingly
# Case 3.1 no args, interpret as reflection requests for particular types (in base case above, request for all types)
./runrpc | ./runrpc
# commander.Commander

# Case 3.2 args, run as command? Not sure yet, placeholder
./runrpc | ./runrpc grep "reflection"
# grpc.reflection.v1.ServerReflection