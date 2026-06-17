package raven
import (
	"bytes"
	"context"
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

type remoteSSH struct {
	client     *ssh.Client
	bouncer    *sshBouncer
	errDomain  string
	emitUpdate func(string)
}

// Alpha discovery!! We can have bash scripts through toml...
// with same security architecture (and metachars too in our commands)
// TODO : implement dial with ctx..
func newRemoteSSH(connInfo *connectionInfo) (*remoteSSH, error) {

	pvKey, err := os.ReadFile(connInfo.KeyPath)
	if err != nil {
		return nil, err
	}

	signer, err := ssh.ParsePrivateKey(pvKey)
	if err != nil {
		return nil, err
	}

	authMethod := ssh.PublicKeys(signer)

	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(connInfo.HostKey))
	if err != nil {
		return nil, err
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
		return nil, err
	}

	bouncer, err := newSSHBouncer()
	if err != nil {
		return nil, err
	}

	return &remoteSSH{
		client:    client,
		bouncer:   bouncer,
		errDomain: string(ToolExecuteSSH) + " error :",
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
			deadLineExceededErr := fmt.Errorf("%s command hung while executing", r.errDomain)
			// yes I want to bonk the ssh conn because time out responsibility is on policy author,
			// if author time out breached it should be treated as error
			r.client.Close()
			return nil, deadLineExceededErr
		} else {
			r.client.Close()
			return nil, fmt.Errorf("%s ssh context cancelled", r.errDomain)
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
		err = fmt.Errorf("%s tool policy retrieval error for tool %s.\nError : %s", r.errDomain, toolName, err.Error())
		return "", err
	}
	return policy, nil
}

func (r *remoteSSH) validateFC(obj *remoteSSHFunctionCall) error {

	if len(obj.Command) == 0 {
		return fmt.Errorf("%s inavlid %s function call args. Shell command name not provided.", r.errDomain, ToolExecuteSSH)
	}

	for i, f := range obj.Flags {
		if len(f.Name) == 0 {
			return fmt.Errorf("%s inavlid %s function call args. %s shell command flag passed without flag name.", r.errDomain, ToolExecuteSSH, ordinal(i+1))
		}
	}

	for i, p := range obj.Positionals {
		if len(p.Value) == 0 {
			return fmt.Errorf("%s inavlid %s function call args. %s shell command positional passed without value.", r.errDomain, ToolExecuteSSH, ordinal(i+1))
		}
		if p.Index == 0 {
			return fmt.Errorf("%s inavlid %s function call args. %s shell command positional index not provided.", r.errDomain, ToolExecuteSSH, ordinal(i+1))
		}
	}

	if len(obj.Reason) == 0 {
		return fmt.Errorf("%s inavlid %s function call args. Tool execution reason not provided.", r.errDomain, ToolExecuteSSH)
	}

	return nil
}

func (r *remoteSSH) callTool(ctx context.Context, fc *llmFunctionCall) (*llmFunctionResponse, *agentErr) {

	var fcObj *remoteSSHFunctionCall

	blob, err := json.Marshal(fc.Args)
	if err != nil {
		err = fmt.Errorf("%s failed to unmarshal function args.\nError : %s", r.errDomain, err.Error())
		return newErrFr(fc, err), nil
	}

	err = json.Unmarshal(blob, &fcObj)
	if err != nil {
		err = fmt.Errorf("%s failed to unmarshal function args.\nError : %s", r.errDomain, err.Error())
		return newErrFr(fc, err), nil
	}

	err = r.validateFC(fcObj)
	if err != nil {
		return newErrFr(fc, err), nil
	}

	r.emitUpdate("validating shell command")
	err = r.bouncer.validate(fcObj)
	if err != nil {
		err = fmt.Errorf("%s shell command validation failed.\nError : %s", r.errDomain, err.Error())
		return newErrFr(fc, err), nil
	}

	r.emitUpdate("constructing shell command")
	cmd, err := r.bouncer.constructCmd(fcObj)
	if err != nil {
		err = fmt.Errorf("%s shell command construction failed.\nError : %s", r.errDomain, err.Error())
		return nil, newAgentError(agentErrTerminate, err)
	}

	out, err := r.execute(ctx, cmd)
	if err != nil {
		err = fmt.Errorf("%s command %s execution failed.\nError : %s", r.errDomain, cmd, err.Error())
		return nil, newAgentError(agentErrTerminate, err)
	}

	outputTemplate := `
	Exit code : {{.ExitCode}}

	Output :
	{{.Output}}
	`

	tmpl, err := template.New("ssh output template").Parse(outputTemplate)
	if err != nil {
		err = fmt.Errorf("%s parsing output template failed\nError : %s", r.errDomain, err.Error())
		return nil, newAgentError(agentErrFatal, err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, out)
	if err != nil {
		err = fmt.Errorf("%s executing output template failed\nError : %s", r.errDomain, err.Error())
		return nil, newAgentError(agentErrFatal, err)
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
	// error dropped on purpose, because
	r.closeConn()
	return nil
}
