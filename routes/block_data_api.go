package routes

import (
	"encoding/json"
	"strconv"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
	"github.com/syndtr/goleveldb/leveldb"

	"github.com/valyala/fasthttp"
)

func GetBlockById(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	blockIdRaw := ctx.UserValue("id")
	blockId, ok := blockIdRaw.(string)

	if !ok {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Invalid value"}`))
		return
	}

	block, err := databases.BLOCKS.Get([]byte(blockId), nil)

	if err == nil && block != nil {
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetContentType("application/json")
		ctx.Write(block)
		return
	}

	ctx.SetStatusCode(fasthttp.StatusNotFound)
	ctx.SetContentType("application/json")
	ctx.Write([]byte(`{"err": "Not found"}`))
}

func GetBlockByHeight(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	absoluteHeightRaw := ctx.UserValue("absoluteHeightIndex")
	absoluteHeight, ok := absoluteHeightRaw.(string)

	if !ok || absoluteHeight == "" {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Invalid height"}`))
		return
	}

	if _, err := strconv.ParseInt(absoluteHeight, 10, 64); err != nil {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Invalid height"}`))
		return
	}

	blockIndexKey := "BLOCK_INDEX:" + absoluteHeight
	blockID, err := databases.STATE.Get([]byte(blockIndexKey), nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			ctx.SetStatusCode(fasthttp.StatusNotFound)
			ctx.SetContentType("application/json")
			ctx.Write([]byte(`{"err": "Not found"}`))
			return
		}

		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to load block id"}`))
		return
	}

	if len(blockID) == 0 {
		ctx.SetStatusCode(fasthttp.StatusNotFound)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Not found"}`))
		return
	}

	block, err := databases.BLOCKS.Get(blockID, nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			ctx.SetStatusCode(fasthttp.StatusNotFound)
			ctx.SetContentType("application/json")
			ctx.Write([]byte(`{"err": "Not found"}`))
			return
		}

		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to load block"}`))
		return
	}

	if len(block) == 0 {
		ctx.SetStatusCode(fasthttp.StatusNotFound)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Not found"}`))
		return
	}

	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json")
	ctx.Write(block)
}

func GetAggregatedFinalizationProof(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	blockIdRaw := ctx.UserValue("blockId")
	blockId, ok := blockIdRaw.(string)

	if !ok {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Invalid value"}`))
		return
	}

	afp, err := databases.EPOCH_DATA.Get([]byte("AFP:"+blockId), nil)

	if err == nil && afp != nil {
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetContentType("application/json")
		ctx.Write(afp)
		return
	}

	ctx.SetStatusCode(fasthttp.StatusNotFound)
	ctx.SetContentType("application/json")
	ctx.Write([]byte(`{"err": "Not found"}`))
}

func GetTransactionByHash(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	hashRaw := ctx.UserValue("hash")
	hash, ok := hashRaw.(string)

	if !ok || hash == "" {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Invalid value"}`))
		return
	}

	locationBytes, err := databases.STATE.Get([]byte("TX:"+hash), nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			ctx.SetStatusCode(fasthttp.StatusNotFound)
			ctx.SetContentType("application/json")
			ctx.Write([]byte(`{"err": "Not found"}`))
			return
		}

		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to load transaction location"}`))
		return
	}

	var location structures.TransactionLocation

	if err := json.Unmarshal(locationBytes, &location); err != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to parse transaction location"}`))
		return
	}

	if location.Block == "" {
		ctx.SetStatusCode(fasthttp.StatusNotFound)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Not found"}`))
		return
	}

	blockBytes, err := databases.BLOCKS.Get([]byte(location.Block), nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			ctx.SetStatusCode(fasthttp.StatusNotFound)
			ctx.SetContentType("application/json")
			ctx.Write([]byte(`{"err": "Not found"}`))
			return
		}

		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to load block"}`))
		return
	}

	var block block_pack.Block

	if err := json.Unmarshal(blockBytes, &block); err != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to parse block"}`))
		return
	}

	if location.Position < 0 || location.Position >= len(block.Transactions) {
		ctx.SetStatusCode(fasthttp.StatusNotFound)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Not found"}`))
		return
	}

	transactionBytes, err := json.Marshal(block.Transactions[location.Position])

	if err != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to serialize transaction"}`))
		return
	}

	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json")
	ctx.Write(transactionBytes)
}

func AcceptTransaction(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	var transaction structures.Transaction

	if err := json.Unmarshal(ctx.PostBody(), &transaction); err != nil {

		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.Write([]byte(`{"err":"Invalid JSON"}`))
		return

	}

	if transaction.From == "" || transaction.Nonce == 0 || transaction.Sig == "" {

		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.Write([]byte(`{"err":"Event structure is wrong"}`))
		return

	}

	currentLeader := utils.GetCurrentLeader()

	if !currentLeader.IsMeLeader {

		// Redirect tx to leader

		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		resp := fasthttp.AcquireResponse()
		defer fasthttp.ReleaseResponse(resp)

		req.SetRequestURI(currentLeader.Url + "/transaction")
		req.Header.SetMethod(fasthttp.MethodPost)
		req.SetBody(ctx.PostBody())

		if err := fasthttp.Do(req, resp); err != nil {
			ctx.SetStatusCode(fasthttp.StatusInternalServerError)
			ctx.Write([]byte(`{"err":"Impossible to redirect to current leader"}`))
			return
		}

		ctx.Write([]byte(`{"status":"Ok, tx redirected to current leader"}`))
		return

	}

	// Check mempool size

	globals.MEMPOOL.Mutex.Lock()
	defer globals.MEMPOOL.Mutex.Unlock()

	if len(globals.MEMPOOL.Slice) >= globals.CONFIGURATION.TxsMempoolSize {
		ctx.SetStatusCode(fasthttp.StatusTooManyRequests)
		ctx.Write([]byte(`{"err":"Mempool is fullfilled"}`))
		return
	}

	// Add to mempool
	globals.MEMPOOL.Slice = append(globals.MEMPOOL.Slice, transaction)

	ctx.Write([]byte(`{"status":"OK"}`))

}
