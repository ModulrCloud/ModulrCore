package databases

import (
	"errors"
	"fmt"

	"github.com/syndtr/goleveldb/leveldb"
)

var BLOCKS, STATE, EPOCH_DATA, APPROVEMENT_THREAD_METADATA, FINALIZATION_VOTING_STATS *leveldb.DB

// Anchors related databases
var ANCHOR_BLOCKS, ANCHOR_EPOCH_DATA, ANCHOR_FINALIZATION_VOTING_STATS *leveldb.DB

// CloseAll safely closes all initialized LevelDB instances
func CloseAll() error {

	type namedDB struct {
		name string
		db   **leveldb.DB
	}

	databases := []namedDB{
		{name: "BLOCKS", db: &BLOCKS},
		{name: "STATE", db: &STATE},
		{name: "EPOCH_DATA", db: &EPOCH_DATA},
		{name: "APPROVEMENT_THREAD_METADATA", db: &APPROVEMENT_THREAD_METADATA},
		{name: "FINALIZATION_VOTING_STATS", db: &FINALIZATION_VOTING_STATS},

		{name: "ANCHOR_BLOCKS", db: &ANCHOR_BLOCKS},
		{name: "ANCHOR_EPOCH_DATA", db: &ANCHOR_EPOCH_DATA},
		{name: "ANCHOR_FINALIZATION_VOTING_STATS", db: &ANCHOR_FINALIZATION_VOTING_STATS},
	}

	var errs []error
	for _, database := range databases {
		if database.db == nil || *database.db == nil {
			continue
		}

		if err := (*database.db).Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", database.name, err))
		}

		*database.db = nil
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}
