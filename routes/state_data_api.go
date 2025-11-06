package routes

import (
	"encoding/json"

	"github.com/ModulrCloud/ModulrCore/databases"
	"github.com/ModulrCloud/ModulrCore/structures"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/valyala/fasthttp"
)

func GetAccountById(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	accountIdRaw := ctx.UserValue("accountId")
	accountId, ok := accountIdRaw.(string)

	if !ok || accountId == "" {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Invalid account id"}`))
		return
	}

	accountBytes, err := databases.STATE.Get([]byte(accountId), nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			ctx.SetStatusCode(fasthttp.StatusNotFound)
			ctx.SetContentType("application/json")
			ctx.Write([]byte(`{"err": "Not found"}`))
			return
		}

		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to load account"}`))
		return
	}

	if len(accountBytes) == 0 {
		ctx.SetStatusCode(fasthttp.StatusNotFound)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Not found"}`))
		return
	}

	var account structures.Account
	if err := json.Unmarshal(accountBytes, &account); err != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to parse account"}`))
		return
	}

	response, err := json.Marshal(account)
	if err != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to encode account"}`))
		return
	}

	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json")
	ctx.Write(response)
}
