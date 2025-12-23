package routes

import (
	"github.com/modulrcloud/modulr-core/databases"

	"github.com/valyala/fasthttp"
)

func GetEpochData(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	epochIndexVal := ctx.UserValue("epochIndex")
	epochIndex, ok := epochIndexVal.(string)

	if !ok {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Invalid epoch index"}`))
		return
	}

	// EPOCH_HANDLER snapshots are stored in APPROVEMENT_THREAD_METADATA DB
	// to be committed atomically with AT updates.
	value, err := databases.APPROVEMENT_THREAD_METADATA.Get([]byte("EPOCH_HANDLER:"+epochIndex), nil)

	if err == nil && value != nil {
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetContentType("application/json")
		ctx.Write(value)
		return
	}

	ctx.SetStatusCode(fasthttp.StatusNotFound)
	ctx.SetContentType("application/json")
	ctx.Write([]byte(`{"err": "No epoch data found"}`))
}
