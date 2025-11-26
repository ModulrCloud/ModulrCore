package routes

import (
	"encoding/json"
	"fmt"

	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/valyala/fasthttp"
)

type delayedTransactionsSignRequest struct {
	EpochIndex int `json:"epochIndex"`
}

type delayedTransactionsSignResponse struct {
	Signature string `json:"signature"`
}

func SignDelayedTransactions(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	var request delayedTransactionsSignRequest
	if err := json.Unmarshal(ctx.PostBody(), &request); err != nil {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.Write([]byte(`{"err":"Invalid JSON"}`))
		return
	}

	if request.EpochIndex < 0 {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.Write([]byte(`{"err":"Invalid epoch index"}`))
		return
	}

	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	epochHandler := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	inQuorum := false
	for _, validatorPubKey := range epochHandler.Quorum {
		if validatorPubKey == globals.CONFIGURATION.PublicKey {
			inQuorum = true
			break
		}
	}

	if !inQuorum {
		ctx.SetStatusCode(fasthttp.StatusForbidden)
		ctx.Write([]byte(`{"err":"Not in quorum"}`))
		return
	}

	delayedTxKey := fmt.Sprintf("DELAYED_TRANSACTIONS:%d", request.EpochIndex)
	payloadBytes, err := databases.STATE.Get([]byte(delayedTxKey), nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			ctx.SetStatusCode(fasthttp.StatusNotFound)
			ctx.Write([]byte(`{"err":"No delayed transactions found"}`))
			return
		}

		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.Write([]byte(`{"err":"Failed to load delayed transactions"}`))
		return
	}

	var cachedPayloads []map[string]string
	if unmarshalErr := json.Unmarshal(payloadBytes, &cachedPayloads); unmarshalErr != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.Write([]byte(`{"err":"Failed to parse delayed transactions"}`))
		return
	}

	if len(cachedPayloads) == 0 {
		ctx.SetStatusCode(fasthttp.StatusNotFound)
		ctx.Write([]byte(`{"err":"No delayed transactions found"}`))
		return
	}

	hashOfPayloads := utils.Blake3(string(payloadBytes))
	signature := cryptography.GenerateSignature(globals.CONFIGURATION.PrivateKey, hashOfPayloads)

	response, _ := json.Marshal(delayedTransactionsSignResponse{Signature: signature})

	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json")
	ctx.Write(response)
}
