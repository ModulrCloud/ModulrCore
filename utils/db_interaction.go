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

func GetAccountFromExecThreadState(accountId string) *structures.Account {

	if val, ok := globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.AccountsCache[accountId]; ok {
		return val
	}

	data, err := globals.STATE.Get([]byte(accountId), nil)

	if err == leveldb.ErrNotFound {

		globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.AccountsCache[accountId] = &structures.Account{}

		return globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.AccountsCache[accountId]

	}

	if err == nil {

		var account structures.Account

		parseErr := json.Unmarshal(data, &account)

		if parseErr == nil {

			globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.AccountsCache[accountId] = &structures.Account{}

			return globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.AccountsCache[accountId]

		}

	}

	return nil

}

func GetValidatorFromApprovementThreadState(validatorPubkey string) *structures.ValidatorStorage {

	validatorStorageKey := validatorPubkey + "_VALIDATOR_STORAGE"

	if val, ok := globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.ValidatorsStoragesCache[validatorStorageKey]; ok {
		return val
	}

	data, err := globals.APPROVEMENT_THREAD_METADATA.Get([]byte(validatorStorageKey), nil)

	if err != nil {
		return nil
	}

	var validatorStorage structures.ValidatorStorage

	err = json.Unmarshal(data, &validatorStorage)

	if err != nil {
		return nil
	}

	globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.ValidatorsStoragesCache[validatorStorageKey] = &validatorStorage

	return &validatorStorage

}

func GetValidatorFromExecThreadState(validatorPubkey string) *structures.ValidatorStorage {

	validatorStorageKey := validatorPubkey + "_VALIDATOR_STORAGE"

	if val, ok := globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.ValidatorsStoragesCache[validatorStorageKey]; ok {
		return val
	}

	data, err := globals.STATE.Get([]byte(validatorStorageKey), nil)

	if err != nil {
		return nil
	}

	var validatorStorage structures.ValidatorStorage

	err = json.Unmarshal(data, &validatorStorage)

	if err != nil {
		return nil
	}

	globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.ValidatorsStoragesCache[validatorStorageKey] = &validatorStorage

	return &validatorStorage

}
