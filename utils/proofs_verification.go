package utils

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/structures"
)

func VerifyAggregatedFinalizationProof(proof *structures.AggregatedFinalizationProof, epochHandler *structures.EpochDataHandler) bool {

	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)

	dataThatShouldBeSigned := strings.Join([]string{proof.PrevBlockHash, proof.BlockId, proof.BlockHash, epochFullID}, ":")

	majority := GetQuorumMajority(epochHandler)

	okSignatures := 0

	seen := make(map[string]bool)

	quorumMap := make(map[string]bool)

	for _, pk := range epochHandler.Quorum {
		quorumMap[strings.ToLower(pk)] = true
	}

	for pubKey, signature := range proof.Proofs {

		if cryptography.VerifySignature(dataThatShouldBeSigned, pubKey, signature) {

			loweredPubKey := strings.ToLower(pubKey)

			if quorumMap[loweredPubKey] && !seen[loweredPubKey] {
				seen[loweredPubKey] = true
				okSignatures++
			}
		}
	}

	return okSignatures >= majority
}

func VerifyAggregatedLeaderFinalizationProof(proof *structures.AggregatedLeaderFinalizationProof, epochHandler *structures.EpochDataHandler, firstBlockHash string) bool {

	if proof == nil || epochHandler == nil || proof.EpochIndex != epochHandler.Id {
		return false
	}

	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)

	majority := GetQuorumMajority(epochHandler)

	quorumMap := make(map[string]bool)
	for _, pk := range epochHandler.Quorum {
		quorumMap[strings.ToLower(pk)] = true
	}

	if firstBlockHash == "" {
		firstBlockHash = structures.NewLeaderVotingStatTemplate().Hash
	}

	if proof.VotingStat.Index >= 0 {
		parts := strings.Split(proof.VotingStat.Afp.BlockId, ":")
		if len(parts) != 3 || parts[0] != strconv.Itoa(epochHandler.Id) || parts[1] != proof.Leader {
			return false
		}

		indexFromId, err := strconv.Atoi(parts[2])
		if err != nil || indexFromId != proof.VotingStat.Index || proof.VotingStat.Hash != proof.VotingStat.Afp.BlockHash {
			return false
		}

		if !VerifyAggregatedFinalizationProof(&proof.VotingStat.Afp, epochHandler) {
			return false
		}
	}

	dataToVerify := strings.Join([]string{"LEADER_ROTATION_PROOF", proof.Leader, firstBlockHash, strconv.Itoa(proof.VotingStat.Index), proof.VotingStat.Hash, epochFullID}, ":")

	okSignatures := 0
	seen := make(map[string]bool)

	for pubKey, signature := range proof.Signatures {
		if cryptography.VerifySignature(dataToVerify, pubKey, signature) {
			lowered := strings.ToLower(pubKey)
			if quorumMap[lowered] && !seen[lowered] {
				seen[lowered] = true
				okSignatures++
			}
		}
	}

	return okSignatures >= majority
}

func VerifyAggregatedAnchorRotationProof(epochIndex int, anchor string, epochHandler *structures.EpochDataHandler, expectedAnchor string) bool {

	if epochHandler == nil {
		return false
	}

	if epochIndex != epochHandler.Id {
		return false
	}

	if !strings.EqualFold(anchor, expectedAnchor) {
		return false
	}

	return true
}

func GetVerifiedAggregatedFinalizationProofByBlockId(blockID string, epochHandler *structures.EpochDataHandler) *structures.AggregatedFinalizationProof {

	localAfpAsBytes, err := databases.EPOCH_DATA.Get([]byte("AFP:"+blockID), nil)

	if err == nil {

		var localAfpParsed structures.AggregatedFinalizationProof

		err = json.Unmarshal(localAfpAsBytes, &localAfpParsed)

		if err != nil {
			return nil
		}

		return &localAfpParsed

	}

	quorum := GetQuorumUrlsAndPubkeys(epochHandler)

	resultChan := make(chan *structures.AggregatedFinalizationProof, len(quorum))

	var wg sync.WaitGroup

	ctx, cancel := context.WithCancel(context.Background())

	defer cancel() // ensure cancellation if function exits early

	for _, node := range quorum {

		wg.Add(1)

		go func(endpoint string) {

			defer wg.Done()

			req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/aggregated_finalization_proof/"+blockID, nil)
			if err != nil {
				return
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			var afp structures.AggregatedFinalizationProof

			if json.NewDecoder(resp.Body).Decode(&afp) == nil && VerifyAggregatedFinalizationProof(&afp, epochHandler) {

				// send the first valid result and immediately cancel all other requests
				select {

				case resultChan <- &afp:
					cancel() // stop all remaining goroutines and HTTP requests
				default:

				}
			}

		}(node.Url)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Return first valid AFP
	for res := range resultChan {
		if res != nil {
			return res
		}
	}

	return nil
}
