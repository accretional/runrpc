package runner

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type trackedProcess struct {
	pid  int
	cmd  *exec.Cmd // nil for ForkExec processes
	conn net.Conn  // parent's end of the socketpair (nil for Spawn)
	done chan struct{}
}

type runnerServer struct {
	UnimplementedRunnerServer

	mu    sync.Mutex
	procs map[string]*trackedProcess
}

func NewRunnerServer() RunnerServer {
	return &runnerServer{
		procs: make(map[string]*trackedProcess),
	}
}

func (s *runnerServer) track(pid int, cmd *exec.Cmd, conn net.Conn) *trackedProcess {
	id := strconv.Itoa(pid)
	tp := &trackedProcess{pid: pid, cmd: cmd, conn: conn, done: make(chan struct{})}
	s.mu.Lock()
	s.procs[id] = tp
	s.mu.Unlock()
	return tp
}

func (s *runnerServer) Fork(ctx context.Context, req *ForkRequest) (*Process, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "resolve self: %v", err)
	}

	// Create a socketpair. The child gets fds[1] as its fd 3,
	// the parent keeps fds[0] to talk to the child.
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "socketpair: %v", err)
	}

	// Don't let the child's fd be closed on exec.
	syscall.CloseOnExec(fds[0])

	pid, err := syscall.ForkExec(self, os.Args, &syscall.ProcAttr{
		Env: os.Environ(),
		Files: []uintptr{
			os.Stdin.Fd(),
			os.Stdout.Fd(),
			os.Stderr.Fd(),
			uintptr(fds[1]), // child fd 3
		},
	})
	// Close the child's end in the parent regardless.
	syscall.Close(fds[1])

	if err != nil {
		syscall.Close(fds[0])
		return nil, status.Errorf(codes.Internal, "fork: %v", err)
	}

	// Wrap the parent's end as a net.Conn.
	parentFile := os.NewFile(uintptr(fds[0]), "fork-parent")
	parentConn, err := net.FileConn(parentFile)
	parentFile.Close()
	if err != nil {
		syscall.Kill(pid, syscall.SIGKILL)
		return nil, status.Errorf(codes.Internal, "wrap socket: %v", err)
	}

	tp := s.track(pid, nil, parentConn)
	go waitPid(pid, tp)

	log.Printf("[runner] forked pid %d (parent %d, fd 3 = socketpair)", pid, os.Getpid())
	return &Process{Pid: int32(pid)}, nil
}

// waitPid blocks until the given pid exits and then closes tp.done.
func waitPid(pid int, tp *trackedProcess) {
	for {
		var ws syscall.WaitStatus
		_, err := syscall.Wait4(pid, &ws, 0, nil)
		if err == syscall.EINTR {
			continue
		}
		break
	}
	close(tp.done)
	log.Printf("[runner] process %d exited", pid)
}

// ConnForPid returns the parent's socketpair connection to a forked
// child. This lets callers set up a gRPC client to the child.
// Returns nil if the process wasn't forked or has no connection.
func (s *runnerServer) ConnForPid(pid string) net.Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tp, ok := s.procs[pid]; ok {
		return tp.conn
	}
	return nil
}

// InheritedConn returns a net.Conn wrapping fd 3 if it exists. A forked
// child calls this to get its end of the socketpair.
func InheritedConn() (net.Conn, error) {
	f := os.NewFile(3, "fork-child")
	if f == nil {
		return nil, fmt.Errorf("fd 3 not available")
	}
	conn, err := net.FileConn(f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("fd 3 is not a socket: %w", err)
	}
	return conn, nil
}

func (s *runnerServer) Spawn(ctx context.Context, req *SpawnRequest) (*Process, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path cannot be empty")
	}

	cmd := exec.Command(req.Path, req.Args...)

	if len(req.Env) > 0 {
		env := make([]string, 0, len(req.Env))
		for k, v := range req.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = env
	} else {
		cmd.Env = os.Environ()
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, status.Errorf(codes.Internal, "spawn %s: %v", req.Path, err)
	}

	pid := cmd.Process.Pid
	tp := s.track(pid, cmd, nil)

	go func() {
		cmd.Wait()
		close(tp.done)
		log.Printf("[runner] process %d exited", pid)
	}()

	log.Printf("[runner] spawned %s pid %d", req.Path, pid)
	return &Process{Pid: int32(pid)}, nil
}

func (s *runnerServer) signal(tp *trackedProcess, sig syscall.Signal) error {
	if tp.cmd != nil {
		return tp.cmd.Process.Signal(sig)
	}
	return syscall.Kill(tp.pid, sig)
}

func (s *runnerServer) Stop(ctx context.Context, req *StopRequest) (*StopResponse, error) {
	if req.ProcessId == "" {
		return nil, status.Error(codes.InvalidArgument, "process_id cannot be empty")
	}

	s.mu.Lock()
	tp, ok := s.procs[req.ProcessId]
	s.mu.Unlock()

	if !ok {
		return nil, status.Errorf(codes.NotFound, "process %s not tracked", req.ProcessId)
	}

	// Already exited?
	select {
	case <-tp.done:
		exitCode := s.exitCode(tp)
		s.remove(req.ProcessId)
		return &StopResponse{ProcessId: req.ProcessId, Success: true, ExitCode: int32(exitCode)}, nil
	default:
	}

	sig := syscall.SIGTERM
	if req.Force {
		sig = syscall.SIGKILL
	}
	if err := s.signal(tp, sig); err != nil {
		return nil, status.Errorf(codes.Internal, "signal %s: %v", req.ProcessId, err)
	}

	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	select {
	case <-tp.done:
	case <-time.After(timeout):
		if !req.Force {
			s.signal(tp, syscall.SIGKILL)
			<-tp.done
		}
	}

	exitCode := s.exitCode(tp)
	s.remove(req.ProcessId)

	log.Printf("[runner] stopped process %s (exit %d)", req.ProcessId, exitCode)
	return &StopResponse{ProcessId: req.ProcessId, Success: true, ExitCode: int32(exitCode)}, nil
}

func (s *runnerServer) exitCode(tp *trackedProcess) int {
	if tp.cmd != nil && tp.cmd.ProcessState != nil {
		return tp.cmd.ProcessState.ExitCode()
	}
	return -1
}

func (s *runnerServer) remove(id string) {
	s.mu.Lock()
	tp := s.procs[id]
	delete(s.procs, id)
	s.mu.Unlock()
	if tp != nil && tp.conn != nil {
		tp.conn.Close()
	}
}
