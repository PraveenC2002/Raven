package main

import (
	"errors"
	"net"
	"os"
	"strconv"

	"golang.org/x/crypto/ssh"
)

type diagnoser struct {
	client *ssh.Client
}

func newDiagnoser(connInfo *connectionInfo) (*diagnoser, error) {

	pvKey, err := os.ReadFile(connInfo.keyPath)
	if err != nil {
		return nil, err
	}

	signer, err := ssh.ParsePrivateKey(pvKey)
	if err != nil {
		return nil, err
	}

	authMethod := ssh.PublicKeys(signer)

	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(connInfo.hostKey))
	if err != nil {
		return nil, err
	}
	
	hostKeyCallBack := ssh.FixedHostKey(hostKey)

	clientConf := ssh.ClientConfig{
		User: connInfo.sshUser,
		Auth: []ssh.AuthMethod{authMethod},
		HostKeyCallback: hostKeyCallBack,
		Timeout: sshClientTimeout,
	}

	addr := net.JoinHostPort(connInfo.host, strconv.Itoa(connInfo.port))
	
	client, err := ssh.Dial("tcp", addr, &clientConf)
	if err != nil {
		return nil, err
	}
	
	return &diagnoser{
		client: client,
	}, nil
}


// TODO: shell command timeout
func (d *diagnoser) execute(cmd string) (*diagnoseResult, error) {

	sess, err := d.client.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()

	res, err := sess.CombinedOutput(cmd)
	resText := string(res)

	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		return &diagnoseResult{
			output: resText,
			exitCode: exitErr.ExitStatus(),
		}, nil
	}

	if err != nil {
		return nil , err
	}

	return &diagnoseResult{
		output: resText,
		exitCode: 0,
	}, nil

}

func (d *diagnoser) closeConn() error {
	return d.client.Close()
}