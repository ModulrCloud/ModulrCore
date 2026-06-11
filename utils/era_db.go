package utils

import (
	"sync"

	"github.com/modulrcloud/modulr-core/globals"

	"github.com/syndtr/goleveldb/leveldb"
)

// Era-scoped LevelDB handles.
//
// During a scheduled recovery transition the execution cursor still points at
// the previous ("old era") network while the live consensus runs on the
// recovered genesis network. Catch-up therefore needs to read the previous
// network's network-scoped databases (BLOCKS, FINALIZATION_THREAD_METADATA,
// ...), which live under <chaindata>/<oldNetworkId>/<dbName> and are NOT the
// process-wide handles in the databases package.
//
// Opening a LevelDB file takes an exclusive on-disk lock, so the same path must
// not be opened twice in-process. We therefore:
//   - never open the active genesis network here (callers use the shared
//     databases.* handles for that);
//   - cache one handle per (dbName, networkId) for old-era paths so concurrent
//     readers (execution thread + HTTP serve routes) share a single handle.
var (
	eraDbMutex sync.Mutex
	eraDbCache = make(map[string]*leveldb.DB)
)

// OpenNetworkScopedDb returns a cached LevelDB handle for a network-scoped
// database belonging to networkId. It is intended for reading previous-era data
// during recovery catch-up; callers must use the shared databases.* handles for
// the active genesis network.
func OpenNetworkScopedDb(dbName string, networkId string) (*leveldb.DB, error) {
	cacheKey := networkId + "|" + dbName

	eraDbMutex.Lock()
	defer eraDbMutex.Unlock()

	if db, ok := eraDbCache[cacheKey]; ok {
		return db, nil
	}

	db, err := leveldb.OpenFile(ResolveDbPathForNetwork(dbName, networkId), nil)
	if err != nil {
		return nil, err
	}

	eraDbCache[cacheKey] = db

	return db, nil
}

// IsActiveNetworkId reports whether networkId refers to the live genesis
// network (or is empty, which is treated as the active network for backward
// compatibility). Old-era reads/serves are only required when this is false.
func IsActiveNetworkId(networkId string) bool {
	return networkId == "" || networkId == globals.GENESIS.NetworkId
}

// CloseNetworkScopedDbs closes and forgets all cached old-era handles.
func CloseNetworkScopedDbs() {
	eraDbMutex.Lock()
	defer eraDbMutex.Unlock()

	for key, db := range eraDbCache {
		if db != nil {
			_ = db.Close()
		}
		delete(eraDbCache, key)
	}
}
