package api

import (
	"sync"
	"time"

	"github.com/eigeninference/d-inference/coordinator/protocol"
)

const (
	// DefaultChallengeInterval is how often the coordinator challenges providers.
	DefaultChallengeInterval = 5 * time.Minute

	// ChallengeResponseTimeout is how long to wait for a challenge response.
	ChallengeResponseTimeout = 30 * time.Second

	// MaxConsecutiveChallengeTimeoutsBeforeReconnect is the number of consecutive
	// transient challenge timeouts (no response within ChallengeResponseTimeout)
	// after which the coordinator force-closes the provider's WebSocket so it must
	// reconnect and re-register.
	//
	// MarkUntrustedTransient keeps challenging a provider in place so it can
	// self-recover via a later passing challenge — but that only helps if the
	// provider can actually send a response. A provider whose outbound path is
	// wedged keeps heartbeating (so it is never evicted by the stale sweeper)
	// while failing every challenge, leaving it pinned hardware/untrusted forever.
	// Cycling the connection forces a clean re-registration, which is the only way
	// back. Must be > MaxFailedChallenges so a brief blip (sleep/network) still
	// self-recovers without a disconnect.
	MaxConsecutiveChallengeTimeoutsBeforeReconnect = 6
)

// pendingChallenge tracks an outstanding challenge sent to a provider.
type pendingChallenge struct {
	nonce      string
	timestamp  string
	sentAt     time.Time
	responseCh chan *protocol.AttestationResponseMessage
}

// challengeTracker manages pending challenges for provider connections.
type challengeTracker struct {
	mu      sync.Mutex
	pending map[string]*pendingChallenge // keyed by nonce
}

func newChallengeTracker() *challengeTracker {
	return &challengeTracker{
		pending: make(map[string]*pendingChallenge),
	}
}

func (ct *challengeTracker) add(nonce string, pc *pendingChallenge) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.pending[nonce] = pc
}

func (ct *challengeTracker) remove(nonce string) *pendingChallenge {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	pc := ct.pending[nonce]
	delete(ct.pending, nonce)
	return pc
}

// codeAttestTracker tracks the outstanding APNs code-identity challenge for a
// connection — the WebSocket return leg of the push round-trip. It is SEPARATE
// from challengeTracker (which is the liveness/SE challenge) so the two flows
// never collide. Keyed by the base64 nonce we pushed.
type codeAttestTracker struct {
	mu      sync.Mutex
	pending map[string]chan *protocol.CodeAttestationResponseMessage
}

func newCodeAttestTracker() *codeAttestTracker {
	return &codeAttestTracker{pending: make(map[string]chan *protocol.CodeAttestationResponseMessage)}
}

func (t *codeAttestTracker) await(nonce string) chan *protocol.CodeAttestationResponseMessage {
	ch := make(chan *protocol.CodeAttestationResponseMessage, 1)
	t.mu.Lock()
	t.pending[nonce] = ch
	t.mu.Unlock()
	return ch
}

func (t *codeAttestTracker) cancel(nonce string) {
	t.mu.Lock()
	delete(t.pending, nonce)
	t.mu.Unlock()
}

func (t *codeAttestTracker) deliver(resp *protocol.CodeAttestationResponseMessage) {
	if resp == nil {
		return
	}
	t.mu.Lock()
	ch := t.pending[resp.Nonce]
	delete(t.pending, resp.Nonce)
	t.mu.Unlock()
	if ch != nil {
		ch <- resp
	}
}
