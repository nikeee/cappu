package dapserver

// Spawn the debuggee JVM with the JDWP agent listening, and report the port it
// chose. server=y,suspend=y,address=127.0.0.1:0 makes the JVM listen on an
// ephemeral loopback port and freeze before main runs; the agent prints the
// chosen port on stdout, which we parse so the session can attach. Because the
// VM is suspended, no program output follows until the caller resumes it, so
// consuming just this line off stdout loses nothing. Port of
// src/services/dap/launch.ts.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
)

const jdwpAgentArg = "-agentlib:jdwp=transport=dt_socket,server=y,suspend=y,address=127.0.0.1:0"

var listeningRe = regexp.MustCompile(`Listening for transport dt_socket at address:\s*(\d+)`)

// Launched is a started debuggee. Stdout continues past the consumed listening
// line (program output once resumed); Stderr is the raw stderr stream.
type Launched struct {
	Cmd    *exec.Cmd
	Stdout *bufio.Reader
	Stderr io.ReadCloser
	Port   int
}

// LaunchOptions are the caller-supplied launch-request knobs.
type LaunchOptions struct {
	VMArgs      []string
	ProgramArgs []string
	Env         map[string]string
	Cwd         string
}

// DebuggeeJavaArgs is the full java argument vector: the JDWP agent, then the
// caller's JVM args, then classpath and main class, then program args. Pure (no
// spawning) so the ordering is unit-testable. Port of debuggeeJavaArgs.
func DebuggeeJavaArgs(classPath, mainClass string, opts LaunchOptions) []string {
	args := []string{jdwpAgentArg}
	args = append(args, opts.VMArgs...)
	args = append(args, "-cp", classPath, mainClass)
	return append(args, opts.ProgramArgs...)
}

func LaunchUnderJdwp(java, classPath, mainClass string, opts LaunchOptions) (*Launched, error) {
	cmd := exec.Command(java, DebuggeeJavaArgs(classPath, mainClass, opts)...)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	if len(opts.Env) > 0 {
		env := os.Environ() // merge over the inherited environment
		for k, v := range opts.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	stdout := bufio.NewReader(stdoutPipe)
	for {
		line, err := stdout.ReadString('\n')
		if m := listeningRe.FindStringSubmatch(line); m != nil {
			port, _ := strconv.Atoi(m[1])
			return &Launched{Cmd: cmd, Stdout: stdout, Stderr: stderrPipe, Port: port}, nil
		}
		if err != nil {
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("debuggee exited before the JDWP agent listened: %w", err)
		}
	}
}
