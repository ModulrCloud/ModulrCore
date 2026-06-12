package routes

import (
	"encoding/json"

	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/http_pack/helpers"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/valyala/fasthttp"
)

type DelayedTransactionsSignRequest struct {
	NetworkId  string `json:"networkId"`
	EpochIndex int    `json:"epochIndex"`
	Legacy     bool   `json:"legacy,omitempty"`
}

type DelayedTransactionsSignResponse struct {
	Signature string `json:"signature"`
}

func SignDelayedTransactions(ctx *fasthttp.RequestCtx) {
	var request DelayedTransactionsSignRequest
	if err := json.Unmarshal(ctx.PostBody(), &request); err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid JSON")
		return
	}

	if request.EpochIndex < 0 {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid epoch index")
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
		helpers.WriteErr(ctx, fasthttp.StatusForbidden, "Not in quorum")
		return
	}

	networkId := request.NetworkId
	if networkId == "" {
		networkId = globals.GENESIS.NetworkId
	}
	if networkId != globals.GENESIS.NetworkId {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid network id")
		return
	}

	delayedTxKey := utils.DelayedTransactionsKey(networkId, request.EpochIndex)
	if request.Legacy && utils.ShouldReadLegacyDelayedTransactions() {
		delayedTxKey = utils.LegacyDelayedTransactionsKey(request.EpochIndex)
	}
	payloadBytes, err := databases.STATE.Get([]byte(delayedTxKey), nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			helpers.WriteErr(ctx, fasthttp.StatusNotFound, "No delayed transactions found")
			return
		}

		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to load delayed transactions")
		return
	}

	var cachedPayloads []map[string]string
	if unmarshalErr := json.Unmarshal(payloadBytes, &cachedPayloads); unmarshalErr != nil {
		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to parse delayed transactions")
		return
	}

	if len(cachedPayloads) == 0 {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "No delayed transactions found")
		return
	}

	dataThatShouldBeSigned := utils.DelayedTransactionsSigningPayload(networkId, request.EpochIndex, payloadBytes)
	signature := cryptography.GenerateSignature(globals.CONFIGURATION.PrivateKey, dataThatShouldBeSigned)

	helpers.WriteJSON(ctx, fasthttp.StatusOK, DelayedTransactionsSignResponse{Signature: signature})
}
