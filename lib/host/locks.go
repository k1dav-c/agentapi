package host

import "sync"

// keyLocks are mutexes by name, made when first locked and dropped when no
// one holds or waits for them.
type keyLocks struct {
	mu    sync.Mutex
	locks map[string]*keyLock
}

type keyLock struct {
	sync.Mutex
	// users hold the lock or wait for it.
	users int
}

func (k *keyLocks) get(name string) *keyLock {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.locks == nil {
		k.locks = map[string]*keyLock{}
	}
	l := k.locks[name]
	if l == nil {
		l = &keyLock{}
		k.locks[name] = l
	}
	l.users++
	return l
}

func (k *keyLocks) put(name string, l *keyLock) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if l.users--; l.users == 0 {
		delete(k.locks, name)
	}
}

// lock locks name and returns its unlock.
func (k *keyLocks) lock(name string) func() {
	l := k.get(name)
	l.Lock()
	return func() {
		l.Unlock()
		k.put(name, l)
	}
}

// tryLock locks name unless someone holds it.
func (k *keyLocks) tryLock(name string) (func(), bool) {
	l := k.get(name)
	if !l.TryLock() {
		k.put(name, l)
		return nil, false
	}
	return func() {
		l.Unlock()
		k.put(name, l)
	}, true
}
