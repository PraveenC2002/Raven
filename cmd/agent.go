package main

type agent struct {
	session *session
}

func newAgent(session *session) *agent {
	return &agent{
		session: session,
	}
}

func (a *agent) loop(req *request) {

}
