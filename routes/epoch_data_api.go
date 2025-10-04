package routes

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/ModulrCloud/ModulrCore/block_pack"
	"github.com/ModulrCloud/ModulrCore/cryptography"
	"github.com/ModulrCloud/ModulrCore/globals"
	"github.com/ModulrCloud/ModulrCore/structures"
	"github.com/ModulrCloud/ModulrCore/utils"

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

func sendJson(ctx *fasthttp.RequestCtx, payload any) {

	ctx.SetContentType("application/json")

	ctx.SetStatusCode(fasthttp.StatusOK)

	jsonBytes, _ := json.Marshal(payload)

	ctx.SetBody(jsonBytes)

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

	value, err := globals.EPOCH_DATA.Get([]byte("FIRST_BLOCK_ASSUMPTION:"+epochIndex), nil)

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

	value, err := globals.EPOCH_DATA.Get([]byte("AEFP:"+epochIndex), nil)

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

		globals.APPROVEMENT_THREAD_METADATA_HANDLER.RWMutex.RLock()

		defer globals.APPROVEMENT_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

		epochHandler := &globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler

		epochIndex := epochHandler.Id

		localIndexOfLeader := epochHandler.CurrentLeaderIndex

		pubKeyOfCurrentLeader := epochHandler.LeadersSequence[localIndexOfLeader]

		firstBlockIdByThisLeader := strconv.Itoa(epochIndex) + ":" + pubKeyOfCurrentLeader + ":0"

		firstBlockAsBytes, dbErr := globals.BLOCKS.Get([]byte(firstBlockIdByThisLeader), nil)

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

func EpochProposition(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	if string(ctx.Method()) != fasthttp.MethodPost {
		ctx.SetStatusCode(fasthttp.StatusMethodNotAllowed)
		return
	}

	var proposition structures.EpochFinishRequest

	if err := json.Unmarshal(ctx.PostBody(), &proposition); err != nil {
		sendJson(ctx, ErrMsg{Err: "Wrong format"})
		return
	}

	if globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Load() {

		globals.APPROVEMENT_THREAD_METADATA_HANDLER.RWMutex.RLock()

		defer globals.APPROVEMENT_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

		epochHandler := &globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler

		epochIndex := epochHandler.Id

		epochFullID := epochHandler.Hash + "#" + strconv.Itoa(int(epochHandler.Id))

		localIndexOfLeader := epochHandler.CurrentLeaderIndex

		pubKeyOfCurrentLeader := epochHandler.LeadersSequence[localIndexOfLeader]

		fmt.Println("DEBUG: ", epochHandler.LeadersSequence, " => ", localIndexOfLeader)

		if utils.SignalAboutEpochRotationExists(epochIndex) {

			votingMetadataForPool := strconv.Itoa(epochIndex) + ":" + pubKeyOfCurrentLeader

			votingRaw, err := globals.FINALIZATION_VOTING_STATS.Get([]byte(votingMetadataForPool), nil)

			var votingData structures.PoolVotingStat

			if err != nil || votingRaw == nil {

				votingData = structures.NewPoolVotingStatTemplate()

			} else {
				_ = json.Unmarshal(votingRaw, &votingData)
			}

			b, _ := json.MarshalIndent(proposition, "", "  ")
			fmt.Println(string(b))

			if proposition.CurrentLeader == localIndexOfLeader {

				if votingData.Index == proposition.LastBlockProposition.Index && votingData.Hash == proposition.LastBlockProposition.Hash {

					var hashOfFirstBlock string

					blockID := strconv.Itoa(epochIndex) + ":" + pubKeyOfCurrentLeader + ":0"

					if proposition.AfpForFirstBlock.BlockId == blockID && proposition.LastBlockProposition.Index >= 0 {

						if utils.VerifyAggregatedFinalizationProof(&proposition.AfpForFirstBlock, epochHandler) {

							hashOfFirstBlock = proposition.AfpForFirstBlock.BlockHash

						}

					}

					if hashOfFirstBlock == "" {

						sendJson(ctx, ErrMsg{Err: "Can't verify hash"})

						return

					}

					dataToSign := "EPOCH_DONE:" +
						strconv.Itoa(proposition.CurrentLeader) + ":" +
						strconv.Itoa(proposition.LastBlockProposition.Index) + ":" +
						proposition.LastBlockProposition.Hash + ":" +
						hashOfFirstBlock + ":" +
						epochFullID

					response := structures.EpochFinishResponseOk{
						Status: "OK",
						Sig:    cryptography.GenerateSignature(globals.CONFIGURATION.PrivateKey, dataToSign),
					}

					sendJson(ctx, response)

				} else if votingData.Index > proposition.LastBlockProposition.Index {

					response := structures.EpochFinishResponseUpgrade{
						Status:               "UPGRADE",
						CurrentLeader:        localIndexOfLeader,
						LastBlockProposition: votingData,
					}

					fmt.Println("DEBUG: =================== Sending UPGRADE 1 ===================")

					sendJson(ctx, response)

				}

			} else if proposition.CurrentLeader < localIndexOfLeader {

				response := structures.EpochFinishResponseUpgrade{
					Status:               "UPGRADE",
					CurrentLeader:        localIndexOfLeader,
					LastBlockProposition: votingData,
				}

				fmt.Println("DEBUG: =================== Sending UPGRADE 2 ===================")

				sendJson(ctx, response)

			}

		} else {

			sendJson(ctx, ErrMsg{Err: "Too early"})

		}

	} else {

		sendJson(ctx, ErrMsg{Err: "Try later"})

	}

}
