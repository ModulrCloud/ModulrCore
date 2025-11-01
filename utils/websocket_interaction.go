package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/ModulrCloud/ModulrCore/databases"
	"github.com/ModulrCloud/ModulrCore/globals"
	"github.com/ModulrCloud/ModulrCore/structures"

	"github.com/gorilla/websocket"
)

type QuorumWaiter struct {
	responseCh chan QuorumResponse
	done       chan struct{}
	answered   map[string]struct{}
	responses  map[string][]byte
	timer      *time.Timer
	mu         sync.Mutex
	buf        []string
	failed     map[string]struct{}
}

type QuorumResponse struct {
	id  string
	msg []byte
}

const (
	MAX_RETRIES             = 3
	RETRY_INTERVAL          = 200 * time.Millisecond
	POD_READ_WRITE_DEADLINE = 2 * time.Second // timeout for read/write operations for POD (point of distribution)
)

// Guards open/close & replace of PoD conn
var POD_MUTEX sync.Mutex

// Single writer guarantee for PoD
var POD_WRITE_MUTEX sync.Mutex

// Protects concurrent access to wsConnMap (map[string]*websocket.Conn)
var WEBSOCKET_CONNECTION_MUTEX sync.RWMutex

var WEBSOCKET_CONNECTION_WITH_POINT_OF_DISTRIBUTION *websocket.Conn

// Ensures single writer per websocket connection (gorilla/websocket requirement)
var WEBSOCKET_WRITE_MUTEX sync.Map // key: pubkey -> *sync.Mutex

func SendWebsocketMessageToPoD(msg []byte) ([]byte, error) {

	for attempt := 1; attempt <= MAX_RETRIES; attempt++ {

		POD_MUTEX.Lock()

		if WEBSOCKET_CONNECTION_WITH_POINT_OF_DISTRIBUTION == nil {

			conn, err := openWebsocketConnectionWithPoD()

			if err != nil {

				POD_MUTEX.Unlock()

				time.Sleep(RETRY_INTERVAL)

				continue
			}

			WEBSOCKET_CONNECTION_WITH_POINT_OF_DISTRIBUTION = conn

		}

		c := WEBSOCKET_CONNECTION_WITH_POINT_OF_DISTRIBUTION

		POD_MUTEX.Unlock()

		// single writer for this connection
		POD_WRITE_MUTEX.Lock()

		_ = c.SetWriteDeadline(time.Now().Add(POD_READ_WRITE_DEADLINE))

		err := c.WriteMessage(websocket.TextMessage, msg)

		POD_WRITE_MUTEX.Unlock()

		if err != nil {
			POD_MUTEX.Lock()
			_ = c.Close()
			WEBSOCKET_CONNECTION_WITH_POINT_OF_DISTRIBUTION = nil
			POD_MUTEX.Unlock()
			time.Sleep(RETRY_INTERVAL)
			continue
		}

		_ = c.SetReadDeadline(time.Now().Add(POD_READ_WRITE_DEADLINE))
		_, resp, err := c.ReadMessage()

		if err != nil {
			POD_MUTEX.Lock()
			_ = c.Close()
			WEBSOCKET_CONNECTION_WITH_POINT_OF_DISTRIBUTION = nil
			POD_MUTEX.Unlock()
			time.Sleep(RETRY_INTERVAL)
			continue
		}

		return resp, nil
	}

	return nil, fmt.Errorf("failed to send message after %d attempts", MAX_RETRIES)

}

func OpenWebsocketConnectionsWithQuorum(quorum []string, wsConnMap map[string]*websocket.Conn) {
	// Close and remove any existing connections (called once per your note)
	WEBSOCKET_CONNECTION_MUTEX.Lock()
	for id, conn := range wsConnMap {
		if conn != nil {
			_ = conn.Close()
		}
		delete(wsConnMap, id)
	}
	WEBSOCKET_CONNECTION_MUTEX.Unlock()

	// Establish new connections for each validator in the quorum
	for _, validatorPubkey := range quorum {
		// Fetch validator metadata
		raw, err := databases.APPROVEMENT_THREAD_METADATA.Get([]byte(validatorPubkey+"_VALIDATOR_STORAGE"), nil)
		if err != nil {
			continue
		}

		// Parse metadata
		var validatorStorage structures.ValidatorStorage
		if err := json.Unmarshal(raw, &validatorStorage); err != nil {
			continue
		}

		// Skip if no WS URL
		if validatorStorage.WssValidatorUrl == "" {
			continue
		}

		// Dial
		conn, _, err := websocket.DefaultDialer.Dial(validatorStorage.WssValidatorUrl, nil)
		if err != nil {
			continue
		}

		// Store in the shared map under lock
		WEBSOCKET_CONNECTION_MUTEX.Lock()
		wsConnMap[validatorPubkey] = conn
		WEBSOCKET_CONNECTION_MUTEX.Unlock()
	}
}

func NewQuorumWaiter(maxQuorumSize int) *QuorumWaiter {
	return &QuorumWaiter{
		responseCh: make(chan QuorumResponse, maxQuorumSize),
		done:       make(chan struct{}),
		answered:   make(map[string]struct{}, maxQuorumSize),
		responses:  make(map[string][]byte, maxQuorumSize),
		timer:      time.NewTimer(0),
		buf:        make([]string, 0, maxQuorumSize),
		failed:     make(map[string]struct{}),
	}
}

func (qw *QuorumWaiter) SendAndWait(
	ctx context.Context, message []byte, quorum []string,
	wsConnMap map[string]*websocket.Conn, majority int,
) (map[string][]byte, bool) {

	// Reset state
	qw.mu.Lock()
	for k := range qw.answered {
		delete(qw.answered, k)
	}
	for k := range qw.responses {
		delete(qw.responses, k)
	}
	for k := range qw.failed {
		delete(qw.failed, k)
	}
	qw.buf = qw.buf[:0]
	qw.mu.Unlock()

	// Arm/Reset timer
	if !qw.timer.Stop() {
		select {
		case <-qw.timer.C:
		default:
		}
	}
	qw.timer.Reset(time.Second)
	qw.done = make(chan struct{})

	// First send to the whole quorum
	qw.sendMessages(quorum, message, wsConnMap)

	for {
		select {
		case r := <-qw.responseCh:
			qw.mu.Lock()
			if _, ok := qw.answered[r.id]; !ok {
				qw.answered[r.id] = struct{}{}
				qw.responses[r.id] = r.msg
			}
			count := len(qw.answered)
			qw.mu.Unlock()

			if count >= majority {
				close(qw.done)
				// copy responses
				qw.mu.Lock()
				out := make(map[string][]byte, len(qw.responses))
				for k, v := range qw.responses {
					out[k] = v
				}
				qw.mu.Unlock()

				// one-shot reconnect of failed nodes
				qw.reconnectFailed(wsConnMap)
				return out, true
			}

		case <-qw.timer.C:
			// resend to unanswered
			qw.mu.Lock()
			qw.buf = qw.buf[:0]
			for _, id := range quorum {
				if _, ok := qw.answered[id]; !ok {
					qw.buf = append(qw.buf, id)
				}
			}
			qw.mu.Unlock()

			if len(qw.buf) == 0 {
				qw.reconnectFailed(wsConnMap)
				return nil, false
			}
			qw.timer.Reset(time.Second)
			qw.sendMessages(qw.buf, message, wsConnMap)

		case <-ctx.Done():
			qw.reconnectFailed(wsConnMap)
			return nil, false
		}
	}
}

func getWriteMu(id string) *sync.Mutex {
	if m, ok := WEBSOCKET_WRITE_MUTEX.Load(id); ok {
		return m.(*sync.Mutex)
	}
	m := &sync.Mutex{}
	actual, _ := WEBSOCKET_WRITE_MUTEX.LoadOrStore(id, m)
	return actual.(*sync.Mutex)
}

func reconnectOnce(pubkey string, wsConnMap map[string]*websocket.Conn) {

	// Get validator metadata
	raw, err := databases.APPROVEMENT_THREAD_METADATA.Get([]byte(pubkey+"_VALIDATOR_STORAGE"), nil)
	if err != nil {
		return
	}
	var validatorStorage structures.ValidatorStorage
	if err := json.Unmarshal(raw, &validatorStorage); err != nil || validatorStorage.WssValidatorUrl == "" {
		return
	}

	// Try a single dial attempt
	conn, _, err := websocket.DefaultDialer.Dial(validatorStorage.WssValidatorUrl, nil)
	if err != nil {
		return
	}

	// Store back into the shared map under lock
	WEBSOCKET_CONNECTION_MUTEX.Lock()

	wsConnMap[pubkey] = conn

	WEBSOCKET_CONNECTION_MUTEX.Unlock()

}

func (qw *QuorumWaiter) reconnectFailed(wsConnMap map[string]*websocket.Conn) {
	qw.mu.Lock()
	failedCopy := make([]string, 0, len(qw.failed))
	for id := range qw.failed {
		failedCopy = append(failedCopy, id)
	}
	// reset failed set for the next round
	for k := range qw.failed {
		delete(qw.failed, k)
	}
	qw.mu.Unlock()

	for _, id := range failedCopy {
		reconnectOnce(id, wsConnMap)
	}
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
		// Read connection from the shared map under RLock
		WEBSOCKET_CONNECTION_MUTEX.RLock()
		conn, ok := wsConnMap[id]
		WEBSOCKET_CONNECTION_MUTEX.RUnlock()
		if !ok || conn == nil {
			// Mark as failed so we try to reconnect after the round
			qw.mu.Lock()
			qw.failed[id] = struct{}{}
			qw.mu.Unlock()
			continue
		}

		go func(id string, c *websocket.Conn) {
			// Single-writer guard for this websocket
			wmu := getWriteMu(id)
			wmu.Lock()
			err := c.WriteMessage(websocket.TextMessage, msg)
			wmu.Unlock()
			if err != nil {
				// Mark as failed and remove the connection safely
				qw.mu.Lock()
				qw.failed[id] = struct{}{}
				qw.mu.Unlock()

				WEBSOCKET_CONNECTION_MUTEX.Lock()
				_ = c.Close()
				delete(wsConnMap, id)
				WEBSOCKET_CONNECTION_MUTEX.Unlock()
				return
			}

			// Short read deadline for reply
			_ = c.SetReadDeadline(time.Now().Add(time.Second))
			_, raw, err := c.ReadMessage()
			if err != nil {
				// Mark as failed and remove the connection safely
				qw.mu.Lock()
				qw.failed[id] = struct{}{}
				qw.mu.Unlock()

				WEBSOCKET_CONNECTION_MUTEX.Lock()
				_ = c.Close()
				delete(wsConnMap, id)
				WEBSOCKET_CONNECTION_MUTEX.Unlock()
				return
			}

			select {
			case qw.responseCh <- QuorumResponse{id: id, msg: raw}:
			case <-qw.done:
			}
		}(id, conn)
	}
}
