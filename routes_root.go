package main

import (
	"github.com/ModulrCloud/ModulrCore/routes"

	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

func NewRouter() fasthttp.RequestHandler {

	r := router.New()

	r.GET("/block/{id}", routes.GetBlockById)
	r.GET("/height/{absoluteHeightIndex}", routes.GetBlockByHeight)

	r.GET("/account/{accountId}", routes.GetAccountById)

	r.GET("/epoch_data/{epochIndex}", routes.GetEpochData)
	r.POST("/epoch_proposition", routes.EpochProposition)

	r.GET("/aggregated_finalization_proof/{blockId}", routes.GetAggregatedFinalizationProof)
	r.GET("/aggregated_epoch_finalization_proof/{epochIndex}", routes.GetAggregatedEpochFinalizationProof)

	r.GET("/first_block_assumption/{epochIndex}", routes.GetFirstBlockAssumption)

	r.GET("/sequence_alignment", routes.GetSequenceAlignmentData)

	r.GET("/transaction/{hash}", routes.GetTransactionByHash)
	r.POST("/transaction", routes.AcceptTransaction)

	return r.Handler
}
