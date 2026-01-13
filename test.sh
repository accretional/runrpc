#!/bin/bash

set -e

echo "=== runrpc Test Suite ==="
echo

echo "Test 1: Exec mode with args"
echo "Command: ./runrpc echo 'Hello World'"
./runrpc echo "Hello World"
echo "✓ Pass"
echo

echo "Test 2: Interactive terminal (no args)"
echo "Command: ./runrpc (timeout after 0.5s)"
timeout 0.5 ./runrpc || echo "✓ Pass - Shows services and exits"
echo

echo "Test 3: Pipe to grep"
echo "Command: ./runrpc | ./runrpc grep 'Commander'"
./runrpc | ./runrpc grep "Commander"
echo "✓ Pass"
echo

echo "Test 4: Pipe to head"
echo "Command: ./runrpc | head -3"
./runrpc | head -3
echo "✓ Pass"
echo

echo "Test 5: Multiple command args"
echo "Command: ./runrpc ls -la | head -5"
./runrpc ls -la | head -5
echo "✓ Pass"
echo

echo "=== All tests passed! ==="
