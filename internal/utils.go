package raven

import "fmt"

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func ptr[T any](v T) *T {
	return &v
}

func ordinal(n int) string {
	switch n % 100 {
	case 11, 12, 13:
		return fmt.Sprintf("%dth", n)
	default:
		switch n % 10 {
		case 1:
			return fmt.Sprintf("%dst", n)
		case 2:
			return fmt.Sprintf("%dnd", n)
		case 3:
			return fmt.Sprintf("%drd", n)
		default:
			return fmt.Sprintf("%dth", n)
		}
	}
}

func agentToTransportErr(err *agentErr, sessionKey *tgSessionKey, clientMsg string) *transportErr {
	switch err.kind {
	case agentErrFatal:
		return &transportErr{
			kind: transportErrFatal,
			err:  err.Unwrap(),
		}
	case agentErrTerminate, agentErrLlmRetry:
		return &transportErr{
			kind:       transportErrClient,
			err:        err.Unwrap(),
			sessionKey: sessionKey,
			clientMsg:  clientMsg,
		}
	}
	return nil
}
