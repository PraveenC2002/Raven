package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"text/template"

	"golang.org/x/crypto/ssh"
)

// TODO:creating remoteSSH instance per agent... maybe need to re think tool registry
type remoteSSH struct {
	client    *ssh.Client
	bouncer   *sshBouncer
	errPrefix string
}

// Alpha discovery!! We can have bash scripts through toml... with same security architecture (and metachars too in our commands)
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

	return &remoteSSH{
		client:    client,
		errPrefix: ToolExecuteSSH + " error :",
	}, nil
}

// TODO: persist ssh connection without agent having knowledge of the SSH conn
// TODO: shell command timeout
// TODO: we don't run parallel sessions, need to rethink sessions
func (r *remoteSSH) execute(cmd string) (*sshOutput, error) {

	sess, err := r.client.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()

	res, err := sess.CombinedOutput(cmd)
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
	return r.client.Close()
}

// errors in tool call need to be handled properly,
// because some errors need to propagate to LLM and
// some need to be handled in the system itself
// remote ssh implements llmTool interface
// need to validate fc payload after unmarshal

func (r *remoteSSH) returnErrorFr(fc *llmFunctionCall, err error) *llmFunctionResponse {
	errFr := &llmFunctionResponse{
		ID:     fc.ID,
		Name:   fc.Name,
		Result: err.Error(),
	}
	return errFr
}

func (r *remoteSSH) toolCall(fc *llmFunctionCall) (*llmFunctionResponse, *agentErr) {

	var reqObj *remoteSSHFunctionCall

	blob, err := json.Marshal(fc.Args)
	if err != nil {
		err = fmt.Errorf("%s failed to unmarshal function args.\nError : %s", r.errPrefix, err.Error())
		return r.returnErrorFr(fc, err), nil
	}

	err = json.Unmarshal(blob, &reqObj)
	if err != nil {
		err = fmt.Errorf("%s failed to unmarshal function args.\nError : %s", r.errPrefix, err.Error())
		return r.returnErrorFr(fc, err), nil
	}

	err = r.bouncer.validate(reqObj)
	if err != nil {
		err = fmt.Errorf("%s ssh command validation failed.\nError : %s", r.errPrefix, err.Error())
		return r.returnErrorFr(fc, err), nil
	}

	cmd, err := r.bouncer.constructCmd(reqObj)
	if err != nil {
		err = fmt.Errorf("%s ssh command construction failed.\nError : %s", r.errPrefix, err.Error())
		return nil, newAgentError(agentErrTerminate, err)
	}

	out, err := r.execute(cmd)
	if err != nil {
		err = fmt.Errorf("%s command %s execution failed.\nError : %s", r.errPrefix, cmd, err.Error())
		return nil, newAgentError(agentErrTerminate, err)
	}

	outputTemplate := `
	Exit code : {{.ExitCode}}

	Output :
	{{.Output}}
	`

	tmpl, err := template.New("ssh output template").Parse(outputTemplate)
	if err != nil {
		err = fmt.Errorf("%s parsing output template failed\nError : %s", r.errPrefix, err.Error())
		return nil, newAgentError(agentErrFatal, err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, out)
	if err != nil {
		err = fmt.Errorf("%s executing output template failed\nError : %s", r.errPrefix, err.Error())
		return nil, newAgentError(agentErrFatal, err)
	}

	return &llmFunctionResponse{
		ID:     fc.ID,
		Name:   fc.Name,
		Result: buf.String(),
	}, nil
}
