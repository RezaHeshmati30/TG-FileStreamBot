package linkstate

import "sync"

var store = struct {
	sync.RWMutex
	items map[int]int64
}{items: make(map[int]int64)}

// Allows accepts legacy links without state and otherwise only the currently
// active expiry. Negative values represent a revoked link until their absolute
// timestamp has passed.
func Allows(messageID int, expires int64) bool {
	store.RLock()
	state, managed := store.items[messageID]
	store.RUnlock()
	return !managed || state == expires
}

func Replace(items map[int]int64) {
	copy := make(map[int]int64, len(items))
	for id, expiry := range items {
		copy[id] = expiry
	}
	store.Lock()
	store.items = copy
	store.Unlock()
}

func Snapshot() map[int]int64 {
	store.RLock()
	defer store.RUnlock()
	copy := make(map[int]int64, len(store.items))
	for id, expiry := range store.items {
		copy[id] = expiry
	}
	return copy
}
