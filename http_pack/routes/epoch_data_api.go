package routes

import (
	"encoding/json"
	"strconv"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/valyala/fasthttp"
)

type ErrMsg struct {
	Err string `json:"err"`
}

type AlignmentData struct {
	ProposedIndexOfLeader            int                                    `json:"proposedIndexOfLeader"`
	FirstBlockByCurrentLeader        block_pack.Block                       `json:"firstBlockByCurrentLeader"`
	AfpForSecondBlockByCurrentLeader structures.AggregatedFinalizationProof `json:"afpForSecondBlockByCurrentLeader"`
}

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

	value, err := databases.EPOCH_DATA.Get([]byte("EPOCH_HANDLER:"+epochIndex), nil)

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

func GetFirstBlockAssumption(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	epochIndexVal := ctx.UserValue("epochIndex")
	epochIndex, ok := epochIndexVal.(string)

	if !ok {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Invalid epoch index"}`))
		return
	}

	value, err := databases.EPOCH_DATA.Get([]byte("FIRST_BLOCK_ASSUMPTION:"+epochIndex), nil)

	if err == nil && value != nil {
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetContentType("application/json")
		ctx.Write(value)
		return
	}

	ctx.SetStatusCode(fasthttp.StatusNotFound)
	ctx.SetContentType("application/json")
	ctx.Write([]byte(`{"err": "No assumptions found"}`))
}

func GetAggregatedEpochFinalizationProof(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	epochIndexVal := ctx.UserValue("epochIndex")
	epochIndex, ok := epochIndexVal.(string)

	if !ok {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Invalid epoch index"}`))
		return
	}

	value, err := databases.EPOCH_DATA.Get([]byte("AEFP:"+epochIndex), nil)

	if err == nil && value != nil {
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetContentType("application/json")
		ctx.Write(value)
		return
	}

	ctx.SetStatusCode(fasthttp.StatusNotFound)
	ctx.SetContentType("application/json")
	ctx.Write([]byte(`{"err": "No assumptions found"}`))
}

func GetSequenceAlignmentData(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	if globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Load() {

		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()

		defer handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

		epochHandler := &handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler

		epochIndex := epochHandler.Id

		localIndexOfLeader := epochHandler.CurrentLeaderIndex

		pubKeyOfCurrentLeader := epochHandler.LeadersSequence[localIndexOfLeader]

		firstBlockIdByThisLeader := strconv.Itoa(epochIndex) + ":" + pubKeyOfCurrentLeader + ":0"

		firstBlockAsBytes, dbErr := databases.BLOCKS.Get([]byte(firstBlockIdByThisLeader), nil)

		if dbErr == nil {

			var firstBlockParsed block_pack.Block

			parseErr := json.Unmarshal(firstBlockAsBytes, &firstBlockParsed)

			if parseErr == nil {

				secondBlockID := strconv.Itoa(epochIndex) + ":" + pubKeyOfCurrentLeader + ":1"

				afpForSecondBlockByCurrentLeader := utils.GetVerifiedAggregatedFinalizationProofByBlockId(secondBlockID, epochHandler)

				if afpForSecondBlockByCurrentLeader != nil {

					alignmentDataResponse := AlignmentData{
						ProposedIndexOfLeader:            localIndexOfLeader,
						FirstBlockByCurrentLeader:        firstBlockParsed,
						AfpForSecondBlockByCurrentLeader: *afpForSecondBlockByCurrentLeader,
					}

					sendJson(ctx, alignmentDataResponse)

				} else {

					sendJson(ctx, ErrMsg{Err: "No AFP for second block"})

				}

			} else {

				sendJson(ctx, ErrMsg{Err: "No first block"})

			}

		} else {

			sendJson(ctx, ErrMsg{Err: "No first block"})

		}

	} else {

		sendJson(ctx, ErrMsg{Err: "Try later"})

	}

}

func sendJson(ctx *fasthttp.RequestCtx, payload any) {

	ctx.SetContentType("application/json")

	ctx.SetStatusCode(fasthttp.StatusOK)

	jsonBytes, _ := json.Marshal(payload)

	ctx.SetBody(jsonBytes)

}
