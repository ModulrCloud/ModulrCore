package tests

import (
	"encoding/json"
	"testing"

	_ "github.com/modulrcloud/modulr-core/tests/testenv"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/http_pack/helpers"
	"github.com/modulrcloud/modulr-core/http_pack/routes"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

func TestRecoveryLastFinalizedHeightReturns404WithoutTracker(t *testing.T) {
	databases.FINALIZATION_THREAD_METADATA = openTempDB(t)

	ctx := callRecoveryRoute("/recovery/last_finalized_height", func(r *router.Router) {
		r.GET("/recovery/last_finalized_height", routes.GetRecoveryLastFinalizedHeight)
	})

	assertJSONError(t, ctx, fasthttp.StatusNotFound, "No finalized height data available")
}

func TestRecoveryLastFinalizedHeightReturns404WithoutAggregatedProof(t *testing.T) {
	databases.FINALIZATION_THREAD_METADATA = openTempDB(t)
	writeJSONToFinalizationDB(t, constants.DBKeyLastMileFinalizerTracker, utils.LastMileSequenceState{
		NextHeight: 7,
	})

	ctx := callRecoveryRoute("/recovery/last_finalized_height", func(r *router.Router) {
		r.GET("/recovery/last_finalized_height", routes.GetRecoveryLastFinalizedHeight)
	})

	assertJSONError(t, ctx, fasthttp.StatusNotFound, "No aggregated height proof found")
}

func TestRecoveryLastFinalizedHeightReturnsSignedLatestAvailableProof(t *testing.T) {
	configureRecoverySigningKey(t)
	databases.FINALIZATION_THREAD_METADATA = openTempDB(t)

	writeJSONToFinalizationDB(t, constants.DBKeyLastMileFinalizerTracker, utils.LastMileSequenceState{
		NextHeight: 8,
	})
	writeJSONToFinalizationDB(t, constants.DBKeyPrefixAggregatedHeightProof+"6", structures.AggregatedHeightProof{
		AbsoluteHeight: 6,
		BlockId:        "1:leader:3",
		BlockHash:      "block-hash-6",
		EpochId:        1,
		HeightInEpoch:  3,
		Proofs:         map[string]string{"validator": "signature"},
	})

	ctx := callRecoveryRoute("/recovery/last_finalized_height", func(r *router.Router) {
		r.GET("/recovery/last_finalized_height", routes.GetRecoveryLastFinalizedHeight)
	})

	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", ctx.Response.StatusCode(), ctx.Response.Body())
	}

	var signed routes.RecoverySignedResponse
	decodeJSONResponse(t, ctx, &signed)
	assertSignedRecoveryPayload(t, signed)

	var payload routes.RecoveryLastFinalizedHeightPayload
	if err := json.Unmarshal(signed.Payload, &payload); err != nil {
		t.Fatalf("failed to decode signed payload: %v", err)
	}

	if payload.LastHeight != 6 || payload.BlockId != "1:leader:3" || payload.BlockHash != "block-hash-6" || payload.EpochId != 1 {
		t.Fatalf("unexpected recovery height payload: %+v", payload)
	}
	if payload.Proof == nil || payload.Proof.AbsoluteHeight != 6 {
		t.Fatalf("expected embedded height proof for height 6, got %+v", payload.Proof)
	}
}

func TestRecoveryGenesisTemplateReturns404WithoutMetadata(t *testing.T) {
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.Lock()
	handlers.APPROVEMENT_THREAD_METADATA.Handler = structures.ApprovementThreadMetadataHandler{}
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.Unlock()

	ctx := callRecoveryRoute("/recovery/genesis_template", func(r *router.Router) {
		r.GET("/recovery/genesis_template", routes.GetRecoveryGenesisTemplate)
	})

	assertJSONError(t, ctx, fasthttp.StatusNotFound, "No approvement thread metadata available")
}

func TestRecoveryGenesisTemplateReturnsSignedValidatorSnapshot(t *testing.T) {
	configureRecoverySigningKey(t)
	setupApprovementHandler(t, structures.NetworkParameters{QuorumSize: 1})

	validator := structures.ValidatorStorage{
		Pubkey:          "validator1",
		Percentage:      100,
		ValidatorUrl:    "http://validator",
		WssValidatorUrl: "ws://validator",
	}
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.Lock()
	handlers.APPROVEMENT_THREAD_METADATA.Handler.CoreMajorVersion = 3
	handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler = structures.EpochDataHandler{
		Id:                 9,
		Hash:               "epoch-hash-9",
		ValidatorsRegistry: []string{validator.Pubkey},
	}
	handlers.APPROVEMENT_THREAD_METADATA.ValidatorsStoragesCache[constants.DBKeyPrefixValidatorStorage+validator.Pubkey] = &validator
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.Unlock()

	ctx := callRecoveryRoute("/recovery/genesis_template", func(r *router.Router) {
		r.GET("/recovery/genesis_template", routes.GetRecoveryGenesisTemplate)
	})

	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", ctx.Response.StatusCode(), ctx.Response.Body())
	}

	var signed routes.RecoverySignedResponse
	decodeJSONResponse(t, ctx, &signed)
	assertSignedRecoveryPayload(t, signed)

	var payload structures.RecoveryGenesisTemplatePayload
	if err := json.Unmarshal(signed.Payload, &payload); err != nil {
		t.Fatalf("failed to decode signed payload: %v", err)
	}

	if payload.SourceEpochId != 9 || payload.SourceEpochHash != "epoch-hash-9" || payload.CoreMajorVersion != 3 {
		t.Fatalf("unexpected recovery genesis template payload: %+v", payload)
	}
	if len(payload.Validators) != 1 || payload.Validators[0].Pubkey != validator.Pubkey {
		t.Fatalf("unexpected validators in recovery genesis template: %+v", payload.Validators)
	}
}

func callRecoveryRoute(path string, register func(*router.Router)) *fasthttp.RequestCtx {
	r := router.New()
	register(r)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI(path)
	r.Handler(ctx)

	return ctx
}

func writeJSONToFinalizationDB(t *testing.T, key string, value any) {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("failed to marshal %s: %v", key, err)
	}
	if err := databases.FINALIZATION_THREAD_METADATA.Put([]byte(key), raw, nil); err != nil {
		t.Fatalf("failed to write %s: %v", key, err)
	}
}

func configureRecoverySigningKey(t *testing.T) {
	t.Helper()

	keyPair := cryptography.GenerateKeyPair("", "", nil)
	globals.CONFIGURATION.PublicKey = keyPair.Pub
	globals.CONFIGURATION.PrivateKey = keyPair.Prv
}

func assertSignedRecoveryPayload(t *testing.T, signed routes.RecoverySignedResponse) {
	t.Helper()

	if signed.PubKey != globals.CONFIGURATION.PublicKey {
		t.Fatalf("unexpected signing pubkey: got %q want %q", signed.PubKey, globals.CONFIGURATION.PublicKey)
	}
	if len(signed.Payload) == 0 || signed.Signature == "" {
		t.Fatalf("expected non-empty signed recovery response, got %+v", signed)
	}
	if !cryptography.VerifySignature(string(signed.Payload), signed.PubKey, signed.Signature) {
		t.Fatalf("recovery response signature does not verify")
	}
}

func assertJSONError(t *testing.T, ctx *fasthttp.RequestCtx, expectedStatus int, expectedMessage string) {
	t.Helper()

	if ctx.Response.StatusCode() != expectedStatus {
		t.Fatalf("expected status %d, got %d body=%s", expectedStatus, ctx.Response.StatusCode(), ctx.Response.Body())
	}

	var body helpers.ErrResponse
	decodeJSONResponse(t, ctx, &body)
	if body.Err != expectedMessage {
		t.Fatalf("expected error %q, got %q", expectedMessage, body.Err)
	}
}

func decodeJSONResponse(t *testing.T, ctx *fasthttp.RequestCtx, out any) {
	t.Helper()

	if err := json.Unmarshal(ctx.Response.Body(), out); err != nil {
		t.Fatalf("failed to decode response body %q: %v", ctx.Response.Body(), err)
	}
}
