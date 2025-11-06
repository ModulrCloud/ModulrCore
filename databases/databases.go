package databases

import "github.com/syndtr/goleveldb/leveldb"

var BLOCKS, STATE, EPOCH_DATA, APPROVEMENT_THREAD_METADATA, FINALIZATION_VOTING_STATS *leveldb.DB

// Anchors related databases
var ANCHOR_BLOCKS, ANCHOR_EPOCH_DATA, ANCHOR_FINALIZATION_VOTING_STATS *leveldb.DB
