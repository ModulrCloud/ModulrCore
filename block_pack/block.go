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

type Block struct {
	Creator      string                   `json:"creator"`
	Time         int64                    `json:"time"`
	Epoch        string                   `json:"epoch"`
	Transactions []structures.Transaction `json:"transactions"`
	ExtraData    ExtraDataToBlock         `json:"extraData"`
	Index        int                      `json:"index"`
	PrevHash     string                   `json:"prevHash"`
	Sig          string                   `json:"sig"`
}

func NewBlock(transactions []structures.Transaction, extraData ExtraDataToBlock, epochFullID string) *Block {
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

func (block *Block) CheckAlrpChainValidity(epochHandler *structures.EpochDataHandler, position int) bool {

	aggregatedLeadersRotationProofsRef := block.ExtraData.AggregatedLeadersRotationProofs

	arrayIndexer := 0

	arrayForIteration := slices.Clone(epochHandler.LeadersSequence[:position])

	slices.Reverse(arrayForIteration) // we need reversed version

	reachedLeaderWhoCreatedAtLeastOneBlock := false

	for _, leaderPubkey := range arrayForIteration {

		if alrpForThisLeader, ok := aggregatedLeadersRotationProofsRef[leaderPubkey]; ok {

			signaIsOk := utils.VerifyAggregatedLeaderRotationProof(leaderPubkey, alrpForThisLeader, epochHandler)

			if signaIsOk {

				arrayIndexer++

				if alrpForThisLeader.SkipIndex >= 0 {

					reachedLeaderWhoCreatedAtLeastOneBlock = true

					break

				}

			} else {

				return false

			}

		} else {

			return false

		}

	}

	if arrayIndexer == position || reachedLeaderWhoCreatedAtLeastOneBlock {

		return true

	}

	return false

}

func (block *Block) ExtendedCheckAlrpChainValidity(epochHandler *structures.EpochDataHandler, position int, dontCheckSigna bool) (bool, map[string]structures.ExecutionStatsPerLeaderSequence) {

	aggregatedLeadersRotationProofsRef := block.ExtraData.AggregatedLeadersRotationProofs

	infoAboutFinalBlocksInThisEpoch := make(map[string]structures.ExecutionStatsPerLeaderSequence)

	arrayIndexer := 0

	arrayForIteration := slices.Clone(epochHandler.LeadersSequence[:position])

	slices.Reverse(arrayForIteration) // we need reversed version

	reachedLeaderWhoCreatedAtLeastOneBlock := false

	for _, leaderPubkey := range arrayForIteration {

		if alrpForThisLeader, ok := aggregatedLeadersRotationProofsRef[leaderPubkey]; ok {

			signaIsOk := dontCheckSigna || utils.VerifyAggregatedLeaderRotationProof(leaderPubkey, alrpForThisLeader, epochHandler)

			if signaIsOk {

				infoAboutFinalBlocksInThisEpoch[leaderPubkey] = structures.ExecutionStatsPerLeaderSequence{
					Index:          alrpForThisLeader.SkipIndex,
					Hash:           alrpForThisLeader.SkipHash,
					FirstBlockHash: alrpForThisLeader.FirstBlockHash,
				}

				arrayIndexer++

				if alrpForThisLeader.SkipIndex >= 0 {

					reachedLeaderWhoCreatedAtLeastOneBlock = true

					break

				}

			} else {

				return false, make(map[string]structures.ExecutionStatsPerLeaderSequence)

			}

		} else {

			return false, make(map[string]structures.ExecutionStatsPerLeaderSequence)

		}

	}

	if arrayIndexer == position || reachedLeaderWhoCreatedAtLeastOneBlock {

		return true, infoAboutFinalBlocksInThisEpoch

	}

	return false, make(map[string]structures.ExecutionStatsPerLeaderSequence)

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
