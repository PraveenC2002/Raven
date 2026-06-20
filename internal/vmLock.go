package raven

import (
	"sync"
)

type vmLockProvider struct {
	getAllVm func() ([]*machine, error)
	mu       *sync.Mutex
	vmMap    map[string]*sync.Mutex
}

func newVmLockProvider(getAllVms func() ([]*machine, error)) (*vmLockProvider, error) {

	lp := &vmLockProvider{
		getAllVm: getAllVms,
		mu:    &sync.Mutex{},
		vmMap: make(map[string]*sync.Mutex),
	}

	if err := lp.bootstrap(); err != nil {
		return nil, err
	}

	return lp, nil
}

func (lp *vmLockProvider) bootstrap() error {

	machines, err := lp.getAllVm()
	if err != nil {
		return err
	}

	for _, m := range machines {
		lp.vmMap[m.Name] = &sync.Mutex{}
	}

	return nil
}

// TODO : trigger bootstrap when a new machine is added through the cli while raven daemon is running

func (lp *vmLockProvider) getLock(machineName string) *sync.Mutex {

	lp.mu.Lock()
	defer lp.mu.Unlock()

	if l, ok := lp.vmMap[machineName]; ok {
		return l
	}

	return nil
}
