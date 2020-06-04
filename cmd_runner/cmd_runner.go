// +build !windows

package cmd_runner

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// CapturingPassThroughWriter is a writer that remembers
// data written to it and passes it to w
type CapturingPassThroughWriter struct {
	buf bytes.Buffer
	w   io.Writer
}

// NewCapturingPassThroughWriter creates new CapturingPassThroughWriter
func NewCapturingPassThroughWriter(w io.Writer) *CapturingPassThroughWriter {
	return &CapturingPassThroughWriter{
		w: w,
	}
}

// Write writes data to the writer, returns number of bytes written and an error
func (w *CapturingPassThroughWriter) Write(d []byte) (int, error) {
	w.buf.Write(d)
	return w.w.Write(d)
}

// Bytes returns bytes written to the writer
func (w *CapturingPassThroughWriter) Bytes() []byte {
	return w.buf.Bytes()
}

func CmdRun(scriptPath string, timeout int) error {
	//by default timeout should be 3s
	if timeout <= 0 {
		timeout = 3
	}
	cmd := exec.Command(strings.TrimRight(scriptPath, "\n"))
	cmd.Env = os.Environ()

	// Set up a process group which will be killed later
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var errStdout, errStderr error
	stdoutIn, _ := cmd.StdoutPipe()
	stderrIn, _ := cmd.StderrPipe()
	stdout := NewCapturingPassThroughWriter(os.Stdout)
	stderr := NewCapturingPassThroughWriter(os.Stderr)

	if err := cmd.Start(); err != nil {
		return err
	}
	fmt.Printf("\nStarting the exection of the script: \n%s with timeout: %ds\n", scriptPath, timeout)

	done := make(chan error, 1)
	go func() {
		_, errStdout = io.Copy(stdout, stdoutIn)
		done <- cmd.Wait()
	}()

	_, errStderr = io.Copy(stderr, stderrIn)

	select {
	case <-time.After(time.Duration(timeout) * time.Second):

		// Get process group which we want to kill
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err != nil {
			return err
		}
		// Send kill to process group
		if err := syscall.Kill(-pgid, 15); err != nil {
			return err
		}
		fmt.Printf("Process killed as timeout(%d) reached\n", timeout)
	case err := <-done:
		if err != nil {
			return fmt.Errorf("process finished with error = %v", err)
		}
	}

	if errStdout != nil || errStderr != nil {
		return fmt.Errorf("failed to capture stdout or stderr")
	}
	fmt.Printf("Execution of the script finished successfully\n")

	return nil
}
