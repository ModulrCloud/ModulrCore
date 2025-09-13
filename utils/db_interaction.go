package utils

import (
	"encoding/json"

	"github.com/ModulrCloud/ModulrCore/globals"
	"github.com/ModulrCloud/ModulrCore/structures"

	"github.com/syndtr/goleveldb/leveldb"
)

func OpenDb(dbName string) *leveldb.DB {

	db, err := leveldb.OpenFile(globals.CHAINDATA_PATH+"/DATABASES/"+dbName, nil)
	if err != nil {
		panic("Impossible to open db : " + dbName + " =>" + err.Error())
	}
	return db
}

func GetFromApprovementThreadState(poolId string) *structures.PoolStorage {

	if val, ok := globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.Cache[poolId]; ok {
		return val
	}

	data, err := globals.APPROVEMENT_THREAD_METADATA.Get([]byte(poolId), nil)

	if err != nil {
		return nil
	}

	var pool structures.PoolStorage

	err = json.Unmarshal(data, &pool)

	if err != nil {
		return nil
	}

	globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.Cache[poolId] = &pool

	return &pool

}

func GetAccountFromExecThreadState(accountId string) *structures.Account {

	if val, ok := globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.AccountsCache[accountId]; ok {
		return val
	}

	data, err := globals.STATE.Get([]byte(accountId), nil)

	if err != nil {

		globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.AccountsCache[accountId] = &structures.Account{}

		return globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.AccountsCache[accountId]

	}

	var account structures.Account

	err = json.Unmarshal(data, &account)

	if err != nil {

		globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.AccountsCache[accountId] = &structures.Account{}

		return globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.AccountsCache[accountId]

	}

	globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.AccountsCache[accountId] = &account

	return &account

}

func GetPoolFromExecThreadState(poolId string) *structures.PoolStorage {

	if val, ok := globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.PoolsCache[poolId]; ok {
		return val
	}

	data, err := globals.STATE.Get([]byte(poolId), nil)

	if err != nil {
		return nil
	}

	var poolStorage structures.PoolStorage

	err = json.Unmarshal(data, &poolStorage)

	if err != nil {
		return nil
	}

	globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.PoolsCache[poolId] = &poolStorage

	return &poolStorage

}
