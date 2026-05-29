package routes

import (
	"encoding/json"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/http_pack/helpers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/valyala/fasthttp"
)

type RecoveryLastFinalizedHeightPayload struct {
	LastHeight int                               `json:"lastHeight"`
	BlockId    string                            `json:"blockId"`
	BlockHash  string                            `json:"blockHash"`
	EpochId    int                               `json:"epochId"`
	Proof      *structures.AggregatedHeightProof `json:"proof"`
}

type RecoverySignedResponse struct {
	PubKey    string          `json:"pubKey"`
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

func GetRecoveryLastFinalizedHeight(ctx *fasthttp.RequestCtx) {
	tracker := utils.LoadLastMileSequenceState(constants.DBKeyLastMileFinalizerTracker)
	if tracker == nil || tracker.NextHeight <= 0 {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "No finalized height data available")
		return
	}

	lastHeight := int(tracker.NextHeight - 1)

	var proof *structures.AggregatedHeightProof
	for h := lastHeight; h >= 0 && h > lastHeight-10; h-- {
		if loadedProof := utils.LoadAggregatedHeightProof(h); loadedProof != nil {
			proof = loadedProof
			break
		}
	}

	if proof == nil {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "No aggregated height proof found")
		return
	}

	payload := RecoveryLastFinalizedHeightPayload{
		LastHeight: proof.AbsoluteHeight,
		BlockId:    proof.BlockId,
		BlockHash:  proof.BlockHash,
		EpochId:    proof.EpochId,
		Proof:      proof,
	}

	writeSignedRecoveryPayload(ctx, payload)
}

func GetRecoveryGenesisTemplate(ctx *fasthttp.RequestCtx) {
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	atHandler := handlers.APPROVEMENT_THREAD_METADATA.Handler
	epochHandler := atHandler.EpochDataHandler
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	if atHandler.CoreMajorVersion < 0 || epochHandler.Hash == "" {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "No approvement thread metadata available")
		return
	}

	validators := make([]structures.ValidatorStorage, 0, len(epochHandler.ValidatorsRegistry))
	for _, validatorPubkey := range epochHandler.ValidatorsRegistry {
		validatorStorage := utils.GetValidatorFromApprovementThreadState(validatorPubkey)
		if validatorStorage == nil {
			helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to load validator storage")
			return
		}
		validators = append(validators, *validatorStorage)
	}

	payload := structures.RecoveryGenesisTemplatePayload{
		SourceEpochId:     epochHandler.Id,
		SourceEpochHash:   epochHandler.Hash,
		CoreMajorVersion:  atHandler.CoreMajorVersion,
		NetworkParameters: atHandler.NetworkParameters.CopyNetworkParameters(),
		Validators:        validators,
	}

	writeSignedRecoveryPayload(ctx, payload)
}

func writeSignedRecoveryPayload(ctx *fasthttp.RequestCtx, payload any) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to marshal payload")
		return
	}

	resp := RecoverySignedResponse{
		PubKey:    globals.CONFIGURATION.PublicKey,
		Payload:   payloadBytes,
		Signature: cryptography.GenerateSignature(globals.CONFIGURATION.PrivateKey, string(payloadBytes)),
	}
	helpers.WriteJSON(ctx, fasthttp.StatusOK, resp)
}
