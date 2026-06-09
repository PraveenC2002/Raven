package main

import (
	"errors"
	"net"
	"os"
	"strconv"

	"golang.org/x/crypto/ssh"
	"google.golang.org/genai"
)

type remoteSSH struct {
	client *ssh.Client
}

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
		client: client,
	}, nil
}

// TODO: shell command timeout
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

func (r *remoteSSH) toolCall(fn *genai.FunctionCall) (any, error) {

	return nil, nil
}
