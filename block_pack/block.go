package block_pack

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ModulrCloud/ModulrCore/cryptography"
	"github.com/ModulrCloud/ModulrCore/globals"
	"github.com/ModulrCloud/ModulrCore/structures"
	"github.com/ModulrCloud/ModulrCore/utils"
)

type ExtraData struct {
	Rest                            map[string]string                                    `json:"rest"`
	AefpForPreviousEpoch            *structures.AggregatedEpochFinalizationProof         `json:"aefpForPreviousEpoch"`
	DelayedTransactionsBatch        structures.DelayedTransactionsBatch                  `json:"delayedTxsBatch"`
	AggregatedLeadersRotationProofs map[string]*structures.AggregatedLeaderRotationProof `json:"aggregatedLeadersRotationProofs"`
}

type Block struct {
	Creator      string                   `json:"creator"`
	Time         int64                    `json:"time"`
	Epoch        string                   `json:"epoch"`
	Transactions []structures.Transaction `json:"transactions"`
	ExtraData    ExtraData                `json:"extraData"`
	Index        int                      `json:"index"`
	PrevHash     string                   `json:"prevHash"`
	Sig          string                   `json:"sig"`
}

func NewBlock(transactions []structures.Transaction, extraData ExtraData, epochFullID string) *Block {
	return &Block{
		Creator:      globals.CONFIGURATION.PublicKey,
		Time:         utils.GetUTCTimestampInMilliSeconds(),
		Epoch:        epochFullID,
		Transactions: transactions,
		ExtraData:    extraData,
		Index:        globals.GENERATION_THREAD_METADATA_HANDLER.NextIndex,
		PrevHash:     globals.GENERATION_THREAD_METADATA_HANDLER.PrevHash,
		Sig:          "",
	}
}

func (block *Block) GetHash() string {

	jsonedTransactions, err := json.Marshal(block.Transactions)
	if err != nil {
		panic("GetHash: failed to marshal transactions: " + err.Error())
	}

	dataToHash := strings.Join([]string{
		block.Creator,
		strconv.FormatInt(block.Time, 10),
		string(jsonedTransactions),
		globals.GENESIS.NetworkId,
		block.Epoch,
		strconv.Itoa(block.Index),
		block.PrevHash,
	}, ":")

	return utils.Blake3(dataToHash)
}

func (block *Block) SignBlock() {

	block.Sig = cryptography.GenerateSignature(globals.CONFIGURATION.PrivateKey, block.GetHash())

}

func (block *Block) VerifySignature() bool {

	return cryptography.VerifySignature(block.GetHash(), block.Creator, block.Sig)

}

func (firstBlockInThisEpochByPool *Block) CheckAlrpChainValidity(epochHandler *structures.EpochDataHandler, position int) bool {

	aggregatedLeadersRotationProofsRef := firstBlockInThisEpochByPool.ExtraData.AggregatedLeadersRotationProofs

	arrayIndexer := 0

	arrayForIteration := slices.Clone(epochHandler.LeadersSequence[:position])

	slices.Reverse(arrayForIteration) // we need reversed version

	bumpedWithPoolWhoCreatedAtLeastOneBlock := false

	for _, poolPubKey := range arrayForIteration {

		if alrpForThisPool, ok := aggregatedLeadersRotationProofsRef[poolPubKey]; ok {

			signaIsOk := utils.VerifyAggregatedLeaderRotationProof(poolPubKey, alrpForThisPool, epochHandler)

			if signaIsOk {

				arrayIndexer++

				if alrpForThisPool.SkipIndex >= 0 {

					bumpedWithPoolWhoCreatedAtLeastOneBlock = true

					break

				}

			} else {

				return false

			}

		} else {

			return false

		}

	}

	if arrayIndexer == position || bumpedWithPoolWhoCreatedAtLeastOneBlock {

		return true

	}

	return false

}

func (firstBlockInThisEpochByPool *Block) ExtendedCheckAlrpChainValidity(epochHandler *structures.EpochDataHandler, position int, dontCheckSigna bool) (bool, map[string]structures.ExecutionStatsPerPool) {

	aggregatedLeadersRotationProofsRef := firstBlockInThisEpochByPool.ExtraData.AggregatedLeadersRotationProofs

	infoAboutFinalBlocksInThisEpoch := make(map[string]structures.ExecutionStatsPerPool)

	arrayIndexer := 0

	arrayForIteration := slices.Clone(epochHandler.LeadersSequence[:position])

	slices.Reverse(arrayForIteration) // we need reversed version

	bumpedWithPoolWhoCreatedAtLeastOneBlock := false

	for _, poolPubKey := range arrayForIteration {

		if alrpForThisPool, ok := aggregatedLeadersRotationProofsRef[poolPubKey]; ok {

			signaIsOk := dontCheckSigna || utils.VerifyAggregatedLeaderRotationProof(poolPubKey, alrpForThisPool, epochHandler)

			if signaIsOk {

				infoAboutFinalBlocksInThisEpoch[poolPubKey] = structures.ExecutionStatsPerPool{
					Index:          alrpForThisPool.SkipIndex,
					Hash:           alrpForThisPool.SkipHash,
					FirstBlockHash: alrpForThisPool.FirstBlockHash,
				}

				arrayIndexer++

				if alrpForThisPool.SkipIndex >= 0 {

					bumpedWithPoolWhoCreatedAtLeastOneBlock = true

					break

				}

			} else {

				return false, make(map[string]structures.ExecutionStatsPerPool)

			}

		} else {

			return false, make(map[string]structures.ExecutionStatsPerPool)

		}

	}

	if arrayIndexer == position || bumpedWithPoolWhoCreatedAtLeastOneBlock {

		return true, infoAboutFinalBlocksInThisEpoch

	}

	return false, make(map[string]structures.ExecutionStatsPerPool)

}

func GetBlock(epochIndex int, blockCreator string, index uint, epochHandler *structures.EpochDataHandler) *Block {

	blockID := strconv.Itoa(epochIndex) + ":" + blockCreator + ":" + strconv.Itoa(int(index))

	blockAsBytes, err := globals.BLOCKS.Get([]byte(blockID), nil)

	if err == nil {

		var blockParsed *Block

		err = json.Unmarshal(blockAsBytes, &blockParsed)

		if err == nil {
			return blockParsed
		}

	}

	// Find from other nodes

	quorumUrlsAndPubkeys := utils.GetQuorumUrlsAndPubkeys(epochHandler)

	var quorumUrls []string

	for _, quorumMember := range quorumUrlsAndPubkeys {

		quorumUrls = append(quorumUrls, quorumMember.Url)

	}

	allKnownNodes := append(quorumUrls, globals.CONFIGURATION.BootstrapNodes...)

	resultChan := make(chan *Block, len(allKnownNodes))
	var wg sync.WaitGroup

	for _, node := range allKnownNodes {

		if node == globals.CONFIGURATION.MyHostname {
			continue
		}

		wg.Add(1)
		go func(endpoint string) {

			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			url := endpoint + "/block/" + blockID
			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				return
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil || resp.StatusCode != http.StatusOK {
				return
			}
			defer resp.Body.Close()

			var block Block

			if err := json.NewDecoder(resp.Body).Decode(&block); err == nil {
				resultChan <- &block
			}

		}(node)

	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	for block := range resultChan {
		if block != nil {
			return block
		}
	}

	return nil
}
