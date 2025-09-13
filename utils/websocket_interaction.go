package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/ModulrCloud/ModulrCore/globals"
	"github.com/ModulrCloud/ModulrCore/structures"

	"github.com/gorilla/websocket"
)

const (
	MAX_RETRIES    = 3
	RETRY_INTERVAL = 200 * time.Millisecond
)

var WEBSOCKET_CONNECTION_WITH_POINT_OF_DISTRIBUTION *websocket.Conn

type QuorumWaiter struct {
	responseCh chan QuorumResponse
	done       chan struct{}
	answered   map[string]bool
	responses  map[string][]byte
	timer      *time.Timer
	mu         sync.Mutex
	buf        []string
}

type QuorumResponse struct {
	id  string
	msg []byte
}

func openWebsocketConnectionWithPoD() (*websocket.Conn, error) {

	u, err := url.Parse(globals.CONFIGURATION.PointOfDistributionWS)

	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("dial error: %w", err)
	}

	return conn, nil
}

func (qw *QuorumWaiter) sendMessages(targets []string, msg []byte, wsConnMap map[string]*websocket.Conn) {

	for _, id := range targets {

		conn, ok := wsConnMap[id]

		if !ok {
			continue
		}

		go func(id string, c *websocket.Conn) {

			if err := c.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}

			_ = c.SetReadDeadline(time.Now().Add(time.Second))
			_, raw, err := c.ReadMessage()

			if err == nil {

				select {

				case qw.responseCh <- QuorumResponse{id: id, msg: raw}:
				case <-qw.done:

				}

			}

		}(id, conn)

	}

}

func SendWebsocketMessageToPoD(msg []byte) ([]byte, error) {

	for attempt := 1; attempt <= MAX_RETRIES; attempt++ {

		if WEBSOCKET_CONNECTION_WITH_POINT_OF_DISTRIBUTION == nil {

			var err error

			WEBSOCKET_CONNECTION_WITH_POINT_OF_DISTRIBUTION, err = openWebsocketConnectionWithPoD()

			if err != nil {

				time.Sleep(RETRY_INTERVAL)

				continue

			}

		}

		err := WEBSOCKET_CONNECTION_WITH_POINT_OF_DISTRIBUTION.WriteMessage(websocket.TextMessage, msg)

		if err != nil {

			WEBSOCKET_CONNECTION_WITH_POINT_OF_DISTRIBUTION.Close()

			WEBSOCKET_CONNECTION_WITH_POINT_OF_DISTRIBUTION = nil

			time.Sleep(RETRY_INTERVAL)

			continue

		}

		_, resp, err := WEBSOCKET_CONNECTION_WITH_POINT_OF_DISTRIBUTION.ReadMessage()

		if err != nil {

			WEBSOCKET_CONNECTION_WITH_POINT_OF_DISTRIBUTION.Close()

			WEBSOCKET_CONNECTION_WITH_POINT_OF_DISTRIBUTION = nil

			time.Sleep(RETRY_INTERVAL)

			continue

		}

		return resp, nil
	}

	return nil, fmt.Errorf("failed to send message after %d attempts", MAX_RETRIES)
}

func OpenWebsocketConnectionsWithQuorum(quorum []string, wsConnMap map[string]*websocket.Conn) {

	// Close and remove any existing connections

	// For safety reasons - close connections even in case websocket handler exists for quorum member in NEW quorum

	for id, conn := range wsConnMap {
		if conn != nil {
			_ = conn.Close()
		}
		delete(wsConnMap, id)
	}

	// Establish new connections for each validator in the quorum
	for _, validatorPubkey := range quorum {
		// Fetch validator metadata from LevelDB
		raw, err := globals.APPROVEMENT_THREAD_METADATA.Get([]byte(validatorPubkey+"(POOL)_STORAGE_POOL"), nil)

		if err != nil {
			continue
		}

		// Parse JSON metadata
		var pool structures.PoolStorage
		if err := json.Unmarshal(raw, &pool); err != nil {
			continue
		}

		// Skip inactive validators or those without WebSocket URL
		if pool.WssPoolUrl == "" {
			continue
		}

		// Open WebSocket connection
		conn, _, err := websocket.DefaultDialer.Dial(pool.WssPoolUrl, nil)

		if err != nil {
			continue
		}

		// Store the new connection in the map
		wsConnMap[validatorPubkey] = conn
	}
}

func NewQuorumWaiter(maxQuorumSize int) *QuorumWaiter {
	return &QuorumWaiter{
		responseCh: make(chan QuorumResponse, maxQuorumSize),
		done:       make(chan struct{}),
		answered:   make(map[string]bool, maxQuorumSize),
		responses:  make(map[string][]byte, maxQuorumSize),
		timer:      time.NewTimer(0),
		buf:        make([]string, 0, maxQuorumSize),
	}
}

func (qw *QuorumWaiter) SendAndWait(
	ctx context.Context,
	message []byte,
	quorum []string,
	wsConnMap map[string]*websocket.Conn,
	majority int,
) (map[string][]byte, bool) {

	// Reset state
	qw.mu.Lock()
	for k := range qw.answered {
		delete(qw.answered, k)
	}
	for k := range qw.responses {
		delete(qw.responses, k)
	}
	qw.buf = qw.buf[:0]
	qw.mu.Unlock()

	if !qw.timer.Stop() {
		select {
		case <-qw.timer.C:
		default:
		}
	}
	qw.timer.Reset(time.Second)
	qw.done = make(chan struct{})

	qw.sendMessages(quorum, message, wsConnMap)

	for {
		select {
		case r := <-qw.responseCh:
			qw.mu.Lock()
			if !qw.answered[r.id] {
				qw.answered[r.id] = true
				qw.responses[r.id] = r.msg
			}
			count := len(qw.answered)
			qw.mu.Unlock()

			if count >= majority {
				close(qw.done)
				// Return copy of responses
				qw.mu.Lock()
				out := make(map[string][]byte, len(qw.responses))
				for k, v := range qw.responses {
					out[k] = v
				}
				qw.mu.Unlock()
				return out, true
			}

		case <-qw.timer.C:
			qw.mu.Lock()
			qw.buf = qw.buf[:0]
			for _, id := range quorum {
				if !qw.answered[id] {
					qw.buf = append(qw.buf, id)
				}
			}
			qw.mu.Unlock()

			if len(qw.buf) == 0 {
				return nil, false
			}
			qw.timer.Reset(time.Second)
			qw.sendMessages(qw.buf, message, wsConnMap)

		case <-ctx.Done():
			return nil, false
		}
	}
}
