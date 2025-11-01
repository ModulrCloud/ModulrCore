package databases

import "github.com/syndtr/goleveldb/leveldb"

var BLOCKS, STATE, EPOCH_DATA, APPROVEMENT_THREAD_METADATA, FINALIZATION_VOTING_STATS *leveldb.DB
