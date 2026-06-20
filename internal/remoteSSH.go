package raven

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"text/template"
	"time"

	"golang.org/x/crypto/ssh"
)

//go:embed assets/templates/remoteSSH/output.tmpl
var sshOutputRaw string
var sshOutputTmpl = template.Must(template.New("ssh output template").Parse(sshOutputRaw))

type remoteSSH struct {
	client     *ssh.Client
	bouncer    *sshBouncer
	emitUpdate func(string)
}

// Alpha discovery!! We can have bash scripts through toml...
// with same security architecture (and metachars too in our commands)
// TODO : implement dial with ctx..
func newRemoteSSH(connInfo *connectionInfo) (*remoteSSH, *agentErr) {

	pvKey, err := os.ReadFile(connInfo.KeyPath)
	if err != nil {
		return nil, &agentErr{kind: agentErrTerminate, err: fmt.Errorf("remote ssh: read private key %q: %w", connInfo.KeyPath, err)}
	}

	signer, err := ssh.ParsePrivateKey(pvKey)
	if err != nil {
		return nil, &agentErr{kind: agentErrTerminate, err: fmt.Errorf("remote ssh: parse private key: %w", err)}
	}

	authMethod := ssh.PublicKeys(signer)

	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(connInfo.HostKey))
	if err != nil {
		return nil, &agentErr{kind: agentErrTerminate, err: fmt.Errorf("remote ssh: parse host key: %w", err)}
	}

	hostKeyCallBack := ssh.FixedHostKey(hostKey)

	clientConf := ssh.ClientConfig{
		User:            connInfo.SshUser,
		Auth:            []ssh.AuthMethod{authMethod},
		HostKeyCallback: hostKeyCallBack,
		Timeout:         sshClientTimeout,
	}

	addr := net.JoinHostPort(connInfo.Host, strconv.Itoa(connInfo.Port))

	client, err := ssh.Dial("tcp", addr, &clientConf)
	if err != nil {
		return nil, &agentErr{kind: agentErrTerminate, err: fmt.Errorf("remote ssh: dial %s: %w", addr, err)}
	}

	bouncer, err := newSSHBouncer()
	if err != nil {
		return nil, &agentErr{kind: agentErrFatal, err: fmt.Errorf("remote ssh: init bouncer: %w", err)}
	}

	return &remoteSSH{
		client:  client,
		bouncer: bouncer,
	}, nil
}

const (
	shellCommandTimeout = 30 * time.Second
)

func (r *remoteSSH) execute(ctx context.Context, cmd string) (*sshOutput, error) {

	r.emitUpdate("creating a ssh session with machine")
	sess, err := r.client.NewSession()
	if err != nil {
		return nil, err
	}

	success := false
	defer func() {
		if success {
			sess.Close()
		}
	}()

	r.emitUpdate("executing shell command")

	// policy author needs to implement command level timeout by themselves
	// If a command level timeout mechanism is not present then a command might be left running on remote machine while the ssh connection is closed
	sshCtx, cancel := context.WithTimeout(ctx, shellCommandTimeout)
	defer cancel()

	done := make(chan struct{}, 1)

	var res []byte

	go func() {
		res, err = sess.CombinedOutput(cmd)
		done <- struct{}{}
	}()

	select {

	case <-done:
		success = true
	case <-sshCtx.Done():
		if errors.Is(sshCtx.Err(), context.DeadlineExceeded) {
			deadLineExceededErr := fmt.Errorf("remote ssh: command timed out")
			// yes I want to bonk the ssh conn because time out responsibility is on policy author,
			// if author time out breached it should be treated as error
			r.client.Close()
			return nil, deadLineExceededErr
		} else {
			r.client.Close()
			return nil, fmt.Errorf("remote ssh: context cancelled")
		}
	}

	resText := string(res)

	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		return &sshOutput{
			Output:   resText,
			ExitCode: exitErr.ExitStatus(),
		}, nil
	}

	if err != nil {
		return nil, err
	}

	return &sshOutput{
		Output:   resText,
		ExitCode: 0,
	}, nil

}

func (r *remoteSSH) closeConn() error {
	r.emitUpdate("closing ssh connection")
	return r.client.Close()
}

// remote ssh implements llmTool interface

func (r *remoteSSH) setUpdateEmitter(emitUpdate func(string)) {
	r.emitUpdate = emitUpdate
}

func (r *remoteSSH) getToolPolicy(toolName llmToolName) (string, error) {
	policy, err := r.bouncer.describe(toolName)
	if err != nil {
		err = fmt.Errorf("remote ssh: get policy for tool %q: %w", toolName, err)
		return "", err
	}
	return policy, nil
}

func (r *remoteSSH) validateFC(obj *remoteSSHFunctionCall) error {

	if len(obj.Command) == 0 {
		return fmt.Errorf("remote ssh: %s function call: missing command name", ToolExecuteSSH)
	}

	for i, f := range obj.Flags {
		if len(f.Name) == 0 {
			return fmt.Errorf("remote ssh: %s function call: %s flag missing name", ToolExecuteSSH, ordinal(i+1))
		}
	}

	for i, p := range obj.Positionals {
		if len(p.Value) == 0 {
			return fmt.Errorf("remote ssh: %s function call: %s positional missing value", ToolExecuteSSH, ordinal(i+1))
		}
		if p.Index == 0 {
			return fmt.Errorf("remote ssh: %s function call: %s positional missing index", ToolExecuteSSH, ordinal(i+1))
		}
	}

	if len(obj.Reason) == 0 {
		return fmt.Errorf("remote ssh: %s function call: missing required field: reason", ToolExecuteSSH)
	}

	return nil
}

func (r *remoteSSH) callTool(ctx context.Context, fc *llmFunctionCall) (*llmFunctionResponse, *agentErr) {

	var fcObj *remoteSSHFunctionCall

	blob, err := json.Marshal(fc.Args)
	if err != nil {
		err = fmt.Errorf("remote ssh: unmarshal function args: %w", err)
		return newErrFr(fc, err), nil
	}

	err = json.Unmarshal(blob, &fcObj)
	if err != nil {
		err = fmt.Errorf("remote ssh: unmarshal function args: %w", err)
		return newErrFr(fc, err), nil
	}

	err = r.validateFC(fcObj)
	if err != nil {
		return newErrFr(fc, err), nil
	}

	r.emitUpdate("validating shell command")
	err = r.bouncer.validate(fcObj)
	if err != nil {
		err = fmt.Errorf("remote ssh: validate command: %w", err)
		return newErrFr(fc, err), nil
	}

	r.emitUpdate("constructing shell command")
	cmd, err := r.bouncer.constructCmd(fcObj)
	if err != nil {
		err = fmt.Errorf("remote ssh: construct command: %w", err)
		return nil, &agentErr{kind: agentErrTerminate, err: err}
	}

	out, err := r.execute(ctx, cmd)
	if err != nil {
		err = fmt.Errorf("remote ssh: execute %q: %w", cmd, err)
		return nil, &agentErr{kind: agentErrTerminate, err: err}
	}

	var buf bytes.Buffer
	err = sshOutputTmpl.Execute(&buf, out)
	if err != nil {
		err = fmt.Errorf("remote ssh: execute output template: %w", err)
		return nil, &agentErr{kind: agentErrFatal, err: err}
	}

	r.emitUpdate("returing shell command output")
	return &llmFunctionResponse{
		ID:   fc.ID,
		Name: fc.Name,
		Action: &llmToolAction{
			Mode:      string(llmTMShell),
			Operation: cmd,
		},
		Result: buf.String(),
	}, nil
}

func (r *remoteSSH) close() error {
	// error dropped on purpose, because we just care about closing our side of closing of socket
	_ = r.closeConn()
	return nil
}
