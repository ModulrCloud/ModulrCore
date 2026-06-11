package block_pack

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/syndtr/goleveldb/leveldb"
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
		Index:        handlers.GENERATION_THREAD_METADATA.NextIndex,
		PrevHash:     handlers.GENERATION_THREAD_METADATA.PrevHash,
		Sig:          "",
	}
}

func (block *Block) GetHash() string {
	return block.GetHashForNetwork(globals.GENESIS.NetworkId)
}

func (block *Block) GetHashForNetwork(networkId string) string {
	jsonedTransactions, err := json.Marshal(block.Transactions)

	if err != nil {
		panic("GetHash: failed to marshal transactions: " + err.Error())
	}

	jsonedExtraData, err := json.Marshal(block.ExtraData)

	if err != nil {
		panic("GetHash: failed to marshal extraData: " + err.Error())
	}

	dataToHash := strings.Join([]string{
		block.Creator,
		strconv.FormatInt(block.Time, 10),
		string(jsonedTransactions),
		string(jsonedExtraData),
		networkId,
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

func (block *Block) VerifySignatureForNetwork(networkId string) bool {
	return cryptography.VerifySignature(block.GetHashForNetwork(networkId), block.Creator, block.Sig)
}

func GetBlock(epochIndex int, blockCreator string, index uint, epochHandler *structures.EpochDataHandler) *Block {

	blockID := strconv.Itoa(epochIndex) + ":" + blockCreator + ":" + strconv.Itoa(int(index))

	blockDb, closeBlockDb := getBlockDbForExecution()
	defer closeBlockDb()

	blockAsBytes, err := blockDb.Get([]byte(blockID), nil)

	if err == nil {
		var blockParsed *Block

		err = json.Unmarshal(blockAsBytes, &blockParsed)

		if err == nil {
			return blockParsed
		}
	}

	return fetchBlockFromPeers(blockID, epochHandler)
}

// GetBlockForConsensus resolves a block strictly within the current consensus
// (genesis) network. Unlike GetBlock it never falls back to the execution
// cursor's network DB. During a pending recovery the execution cursor still
// points at the previous network, and because a recovered genesis restarts
// epochs from 0 with the same validator set, blockIds (epoch:creator:index)
// collide with the pre-recovery chain. Reading the old execution DB would
// therefore return stale blocks for new-network blockIds and poison consensus
// state (last-mile height proofs, first-block detection, sequencing).
func GetBlockForConsensus(epochIndex int, blockCreator string, index uint, epochHandler *structures.EpochDataHandler) *Block {

	blockID := strconv.Itoa(epochIndex) + ":" + blockCreator + ":" + strconv.Itoa(int(index))

	if blockAsBytes, err := databases.BLOCKS.Get([]byte(blockID), nil); err == nil {
		var blockParsed *Block

		if json.Unmarshal(blockAsBytes, &blockParsed) == nil {
			return blockParsed
		}
	}

	return fetchBlockFromPeers(blockID, epochHandler)
}

func fetchBlockFromPeers(blockID string, epochHandler *structures.EpochDataHandler) *Block {
	quorumUrlsAndPubkeys := utils.GetQuorumUrlsAndPubkeys(epochHandler)

	var quorumUrls []string

	for _, quorumMember := range quorumUrlsAndPubkeys {
		quorumUrls = append(quorumUrls, quorumMember.Url)
	}

	allKnownNodes := append(quorumUrls, globals.CONFIGURATION.BootstrapNodes...)

	// During a scheduled recovery transition the execution cursor still points
	// at the previous network era, whose blockIds (epoch:creator:index) collide
	// with the recovered genesis chain. Tag peer requests with that era so peers
	// serve the matching previous-era block instead of a same-id new-era block.
	networkQuery := ""
	if eraNetworkId := executionEraNetworkId(); !utils.IsActiveNetworkId(eraNetworkId) {
		networkQuery = "?networkId=" + eraNetworkId
	}

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

			url := endpoint + "/block/" + blockID + networkQuery
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

func getBlockDbForExecution() (*leveldb.DB, func()) {
	networkId := executionEraNetworkId()

	if utils.IsActiveNetworkId(networkId) {
		return databases.BLOCKS, func() {}
	}

	db, err := utils.OpenNetworkScopedDb("BLOCKS", networkId)
	if err != nil {
		return databases.BLOCKS, func() {}
	}

	// Cached era handle is owned by utils; callers must not close it.
	return db, func() {}
}

// executionEraNetworkId returns the network the execution cursor currently
// points at (the previous era during a scheduled recovery transition, otherwise
// the active genesis network).
func executionEraNetworkId() string {
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	networkId := handlers.EXECUTION_THREAD_METADATA.ChainCursor.NetworkId
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	return networkId
}
