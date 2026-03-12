package pty

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// Winsize matches the kernel's struct winsize.
type Winsize struct {
	Rows   uint16
	Cols   uint16
	Xpixel uint16
	Ypixel uint16
}

// PTY holds both sides of a pseudo-terminal pair and the child process.
type PTY struct {
	Master    *os.File
	slavePath string

	pid       int
	done      chan struct{}
	closeOnce sync.Once

	// If we put the outer terminal into raw mode, we stash the old state here.
	oldTermState *term.State
}

// Start opens a new PTY, forks the given command onto the slave side
// with a proper session and controlling terminal, and returns a *PTY
// whose Master is ready for reading/writing.
//
// argv[0] is the executable path. Pass nil env to inherit.
func Start(argv []string, env []string) (*PTY, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("argv cannot be empty")
	}

	master, slavePath, err := Open()
	if err != nil {
		return nil, err
	}

	slave, err := os.OpenFile(slavePath, os.O_RDWR, 0)
	if err != nil {
		master.Close()
		return nil, fmt.Errorf("open slave %s: %w", slavePath, err)
	}

	// Propagate the outer terminal size to the new PTY if we have one.
	if ws, err := GetSize(os.Stdin.Fd()); err == nil {
		SetSize(slave.Fd(), ws)
	}

	if env == nil {
		env = os.Environ()
	}

	pid, err := syscall.ForkExec(argv[0], argv, &syscall.ProcAttr{
		Env: env,
		Files: []uintptr{
			slave.Fd(),
			slave.Fd(),
			slave.Fd(),
		},
		Sys: &syscall.SysProcAttr{
			Setsid:  true,
			Setctty: true,
			Ctty:    0, // fd 0 in the child (the slave we just mapped)
		},
	})
	slave.Close() // parent must close slave or EOF won't work
	if err != nil {
		master.Close()
		return nil, fmt.Errorf("forkexec: %w", err)
	}

	p := &PTY{
		Master:    master,
		slavePath: slavePath,
		pid:       pid,
		done:      make(chan struct{}),
	}

	go p.wait()
	return p, nil
}

// StartWithFd is like Start but also passes extra file descriptors to the child
// (mapped starting at fd 3). Use this to pass your socketpair alongside the PTY.
func StartWithFd(argv []string, env []string, extraFds ...uintptr) (*PTY, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("argv cannot be empty")
	}

	master, slavePath, err := Open()
	if err != nil {
		return nil, err
	}

	slave, err := os.OpenFile(slavePath, os.O_RDWR, 0)
	if err != nil {
		master.Close()
		return nil, fmt.Errorf("open slave %s: %w", slavePath, err)
	}

	if ws, err := GetSize(os.Stdin.Fd()); err == nil {
		SetSize(slave.Fd(), ws)
	}

	if env == nil {
		env = os.Environ()
	}

	fds := []uintptr{slave.Fd(), slave.Fd(), slave.Fd()}
	fds = append(fds, extraFds...)

	pid, err := syscall.ForkExec(argv[0], argv, &syscall.ProcAttr{
		Env:   env,
		Files: fds,
		Sys: &syscall.SysProcAttr{
			Setsid:  true,
			Setctty: true,
			Ctty:    0,
		},
	})
	slave.Close()
	if err != nil {
		master.Close()
		return nil, fmt.Errorf("forkexec: %w", err)
	}

	p := &PTY{
		Master:    master,
		slavePath: slavePath,
		pid:       pid,
		done:      make(chan struct{}),
	}

	go p.wait()
	return p, nil
}

// wait reaps the child process.
func (p *PTY) wait() {
	for {
		var ws syscall.WaitStatus
		_, err := syscall.Wait4(p.pid, &ws, 0, nil)
		if err == syscall.EINTR {
			continue
		}
		break
	}
	close(p.done)
}

// Done returns a channel that is closed when the child process exits.
func (p *PTY) Done() <-chan struct{} {
	return p.done
}

// Pid returns the child's process id.
func (p *PTY) Pid() int {
	return p.pid
}

// Resize sets the PTY window size and sends SIGWINCH to the child.
func (p *PTY) Resize(rows, cols uint16) error {
	if err := SetSize(p.Master.Fd(), &Winsize{Rows: rows, Cols: cols}); err != nil {
		return err
	}
	return syscall.Kill(p.pid, syscall.SIGWINCH)
}

// Close shuts down the PTY. Sends SIGHUP to the child (same as closing
// a real terminal) and closes the master fd.
func (p *PTY) Close() error {
	var err error
	p.closeOnce.Do(func() {
		syscall.Kill(p.pid, syscall.SIGHUP)
		err = p.Master.Close()
	})
	return err
}

// Proxy connects the PTY bidirectionally to the outer terminal
// (os.Stdin/os.Stdout). It puts the outer terminal into raw mode,
// handles SIGWINCH propagation, and blocks until the child exits.
// Restores the outer terminal on return.
func (p *PTY) Proxy() error {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("make raw: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Propagate SIGWINCH.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	go func() {
		for range sigCh {
			if ws, err := GetSize(os.Stdin.Fd()); err == nil {
				SetSize(p.Master.Fd(), ws)
			}
		}
	}()
	sigCh <- syscall.SIGWINCH

	var wg sync.WaitGroup
	wg.Add(1)

	// stdin → master
	go func() {
		defer wg.Done()
		io.Copy(p.Master, os.Stdin)
	}()

	// master → stdout
	io.Copy(os.Stdout, p.Master)

	wg.Wait()
	return nil
}

// Signal sends a signal to the child process.
func (p *PTY) Signal(sig syscall.Signal) error {
	return syscall.Kill(p.pid, sig)
}
