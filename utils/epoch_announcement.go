package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/structures"
)

type epochAnnouncementProofRequest struct {
	Route         string `json:"route"`
	EpochId       int    `json:"epochId"`
	NextEpochId   int    `json:"nextEpochId"`
	EpochDataHash string `json:"epochDataHash"`
}

type epochAnnouncementProofResponse struct {
	Voter string `json:"voter"`
	Sig   string `json:"sig"`
}

type epochAnnouncementProofStoreRequest struct {
	Route string                                      `json:"route"`
	Proof structures.AggregatedEpochAnnouncementProof `json:"proof"`
}

func AnnounceEpochAfterRotation(epochId, nextEpochId int, prevEpochHandler structures.EpochDataHandler) {
	if nextEpochId <= 0 || nextEpochId != epochId+1 || LoadEpochAnnouncementProof(nextEpochId) != nil {
		return
	}

	if prevEpochHandler.Id != epochId {
		return
	}

	proof := tryCollectEpochAnnouncementProof(epochId, nextEpochId, &prevEpochHandler)
	if proof == nil {
		return
	}

	StoreEpochAnnouncementProof(proof)
	sendEpochAnnouncementProofToPoD(*proof)
	sendEpochAnnouncementProofToAnchors(proof)

	LogWithTime(
		fmt.Sprintf("Epoch announcement proof collected for epoch %d->%d (signatures=%d)", proof.EpochId, proof.NextEpochId, len(proof.Proofs)),
		DEEP_GREEN_COLOR,
	)
}

func tryCollectEpochAnnouncementProof(epochId, nextEpochId int, prevEpochHandler *structures.EpochDataHandler) *structures.AggregatedEpochAnnouncementProof {
	localEpochData := LoadNextEpochData(nextEpochId)
	if prevEpochHandler == nil || localEpochData == nil {
		return nil
	}

	epochDataHash := ComputeEpochDataHash(localEpochData)
	if epochDataHash == "" {
		return nil
	}

	tmpConns, tmpWaiter := openTemporaryEpochAnnouncementQuorumConnections(prevEpochHandler)
	defer closeTemporaryEpochAnnouncementQuorumConnections(tmpConns)

	return tryCollectEpochAnnouncementProofWithConns(epochId, nextEpochId, localEpochData, epochDataHash, prevEpochHandler, tmpConns, tmpWaiter)
}

func tryCollectEpochAnnouncementProofWithConns(
	epochId, nextEpochId int,
	localEpochData *structures.NextEpochDataHandler,
	epochDataHash string,
	prevEpochHandler *structures.EpochDataHandler,
	wsConns map[string]*websocket.Conn,
	waiter *QuorumWaiter,
) *structures.AggregatedEpochAnnouncementProof {
	if prevEpochHandler == nil || waiter == nil || localEpochData == nil || epochDataHash == "" {
		return nil
	}

	majority := GetQuorumMajority(prevEpochHandler)
	request := epochAnnouncementProofRequest{
		Route:         constants.WsRouteSignEpochAnnouncementProof,
		EpochId:       epochId,
		NextEpochId:   nextEpochId,
		EpochDataHash: epochDataHash,
	}

	message, err := json.Marshal(request)
	if err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dataToVerify := BuildEpochAnnouncementProofSigningPayload(epochId, nextEpochId, epochDataHash)
	validateProof := func(id string, raw []byte) bool {
		var response epochAnnouncementProofResponse
		if json.Unmarshal(raw, &response) != nil {
			return false
		}
		if !slices.Contains(prevEpochHandler.Quorum, response.Voter) {
			return false
		}
		return cryptography.VerifySignature(dataToVerify, response.Voter, response.Sig)
	}

	responses, ok := waiter.SendAndWaitValidated(ctx, message, prevEpochHandler.Quorum, wsConns, majority, validateProof)
	if !ok {
		return nil
	}

	proofs := make(map[string]string)
	for _, raw := range responses {
		var response epochAnnouncementProofResponse
		if json.Unmarshal(raw, &response) == nil {
			proofs[response.Voter] = response.Sig
		}
	}

	if len(proofs) < majority {
		return nil
	}

	return &structures.AggregatedEpochAnnouncementProof{
		EpochId:       epochId,
		NextEpochId:   nextEpochId,
		EpochData:     *localEpochData,
		EpochDataHash: epochDataHash,
		Proofs:        proofs,
	}
}

func openTemporaryEpochAnnouncementQuorumConnections(epochHandler *structures.EpochDataHandler) (map[string]*websocket.Conn, *QuorumWaiter) {
	conns := make(map[string]*websocket.Conn)
	guards := NewWebsocketGuards()
	OpenWebsocketConnectionsWithQuorum(epochHandler.Quorum, conns, guards)
	waiter := NewQuorumWaiter(len(epochHandler.Quorum), guards)

	return conns, waiter
}

func closeTemporaryEpochAnnouncementQuorumConnections(conns map[string]*websocket.Conn) {
	for _, conn := range conns {
		if conn != nil {
			_ = conn.Close()
		}
	}
}

func sendEpochAnnouncementProofToPoD(proof structures.AggregatedEpochAnnouncementProof) {
	req := epochAnnouncementProofStoreRequest{Route: constants.WsRouteAcceptAggregatedEpochAnnouncementProof, Proof: proof}
	if reqBytes, err := json.Marshal(req); err == nil {
		if globals.CONFIGURATION.DisablePoDOutbox {
			_, _ = SendWebsocketMessageToPoD(reqBytes)
			return
		}
		_ = SendToPoDWithOutbox(PoDOutboxIdForEpochAnnouncementProof(proof.NextEpochId), reqBytes)
	}
}

func sendEpochAnnouncementProofToAnchors(proof *structures.AggregatedEpochAnnouncementProof) {
	if proof == nil {
		return
	}

	payload := structures.AcceptEpochAnnouncementProofRequest{Proof: *proof}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	for _, anchor := range globals.ANCHORS {
		go func(anchor structures.Anchor) {
			if anchor.AnchorUrl == "" {
				return
			}
			url := fmt.Sprintf("%s/accept_core_epoch_announcement_proof", strings.TrimRight(anchor.AnchorUrl, "/"))
			req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := client.Do(req)
			if err != nil {
				LogWithTimeThrottled(
					fmt.Sprintf("epoch_announcement:anchors:post_err:%d:%s", proof.NextEpochId, anchor.AnchorUrl),
					5*time.Second,
					fmt.Sprintf("Epoch announcement: anchors POST failed (nextEpoch=%d anchor=%s): %v", proof.NextEpochId, anchor.AnchorUrl, err),
					YELLOW_COLOR,
				)
				return
			}
			defer resp.Body.Close()

			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			if resp.StatusCode != http.StatusOK {
				LogWithTimeThrottled(
					fmt.Sprintf("epoch_announcement:anchors:post_bad_status:%d:%s:%d", proof.NextEpochId, anchor.AnchorUrl, resp.StatusCode),
					5*time.Second,
					fmt.Sprintf("Epoch announcement: anchors POST bad status (nextEpoch=%d anchor=%s http=%d body=%s)", proof.NextEpochId, anchor.AnchorUrl, resp.StatusCode, strings.TrimSpace(string(respBody))),
					YELLOW_COLOR,
				)
				return
			}
		}(anchor)
	}
}
