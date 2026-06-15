package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"time"

	"github.com/eigeninference/d-inference/coordinator/attestation"
	"github.com/eigeninference/d-inference/coordinator/registry"
)

// CodeAttestResponseTimeout bounds how long the coordinator waits for the
// provider's WebSocket reply to an APNs code-identity challenge. Generous because
// a background push may be briefly delayed; the provider's local decrypt+sign is
// sub-second.
const CodeAttestResponseTimeout = 90 * time.Second

// codeAttestLoop drives the APNs code-identity round-trip for one connection.
//
// Attestation is PER-CONNECTION: while a WebSocket is alive the provider's binary
// cannot change (a binary swap restarts the process and drops the connection), so
// one successful challenge proves the connection's code identity for its whole
// lifetime — there is NO periodic re-challenge. That also respects Apple's
// background-push budget (~2-3/hour/device); a 5-minute ticker (12/hour) would be
// throttled and dropped.
//
// Reliability without spamming pushes:
//   - Reuse: if this device (Secure Enclave key) attested recently with the same
//     binary version, the new connection inherits the proof with NO push — so a
//     brief reconnect/network blip doesn't burn a push or strand the provider.
//   - Bounded retry: otherwise challenge once, retrying only on delivery failure,
//     spaced by the per-device push cooldown and capped, so a dropped push heals
//     without exceeding the budget.
//
// Providers with no APNs device token (legacy <0.6.0, or headless boxes with no
// GUI session) can never attest, so the loop exits immediately — they are derouted
// once enforcement begins, the intended "everyone must update" outcome.
func (s *Server) codeAttestLoop(ctx context.Context, providerID string, provider *registry.Provider, ct *codeAttestTracker) {
	if s.codeAttestor == nil || provider == nil {
		return
	}

	provider.Mu().Lock()
	hasToken := provider.APNsDeviceToken != ""
	version := provider.Version
	var seKey string
	if provider.AttestationResult != nil {
		seKey = provider.AttestationResult.PublicKey
	}
	provider.Mu().Unlock()
	if !hasToken {
		s.logger.Info("code-attest: provider has no APNs device token; cannot attest (will be derouted once enforcement begins)",
			"provider_id", providerID)
		return
	}

	// Reuse a recent, same-version attestation for this device instead of spending
	// a push — the binary can't have changed (same version) and the proof is fresh.
	if s.codeAttestThrottle.reuseAttestation(seKey, version) {
		provider.SetCodeAttested(true)
		s.registry.DrainQueuedRequestsForProvider(provider)
		s.logger.Info("code-attest: reused a recent attestation for this device (no push)",
			"provider_id", providerID)
		return
	}

	for attempt := 0; attempt < s.codeAttestThrottle.maxAttempts; attempt++ {
		if provider.GetCodeAttested() {
			return // attested for this connection; nothing more to do
		}
		if provider.ChallengeShouldStop() {
			return // hard (non-recoverable) untrust — stop challenging
		}
		// Only push if the per-device cooldown allows it (background-push budget).
		if s.codeAttestThrottle.allowPush(seKey) {
			s.codeAttestThrottle.recordPush(seKey)
			s.sendCodeIdentityChallenge(ctx, providerID, provider, ct)
			if provider.GetCodeAttested() {
				s.codeAttestThrottle.recordAttested(seKey, version)
				return
			}
		}
		// Space retries by the cooldown so we never exceed the background-push
		// budget; bail if the connection ends first.
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.codeAttestThrottle.pushCooldown):
		}
	}
	s.logger.Warn("code-attest: not attested after max attempts; will retry on a later reconnect (within the push budget)",
		"provider_id", providerID)
}

// sendCodeIdentityChallenge runs the per-connection APNs code-identity round-trip
// (v0.6.0): it pushes E_K(nonce) to the provider's device, awaits the provider's
// code_attestation_response over the WebSocket, verifies it (the returned nonce
// equals the one we pushed AND Sign_SE over that nonce verifies against the SE
// public key bound at registration), and marks the connection CodeAttested on
// success. Fail-closed: any failure leaves CodeAttested false, so once the
// rollout flag is on the provider is not routed private traffic. The nonce is a
// base64 string; it is encrypted to the provider's X25519 key K via the same E2E
// path used for inference bodies, and the signature is the SE P-256 key (K is
// decrypt-only — there is no Sign_K). See docs/apns-code-attestation-design.md.
func (s *Server) sendCodeIdentityChallenge(ctx context.Context, providerID string, provider *registry.Provider, ct *codeAttestTracker) {
	if s.codeAttestor == nil || provider == nil {
		return
	}
	provider.Mu().Lock()
	deviceToken := provider.APNsDeviceToken
	env := provider.APNsEnvironment
	pubKey := provider.PublicKey
	var sePubKey string
	if provider.AttestationResult != nil {
		sePubKey = provider.AttestationResult.PublicKey
	}
	provider.Mu().Unlock()

	if deviceToken == "" || pubKey == "" || sePubKey == "" {
		s.logger.Warn("code-attest skipped: missing device token, encryption key, or SE key",
			"provider_id", providerID,
			"has_token", deviceToken != "",
			"has_pubkey", pubKey != "",
			"has_se_key", sePubKey != "",
		)
		return
	}

	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		s.logger.Error("code-attest nonce generation failed", "provider_id", providerID, "error", err)
		return
	}
	nonceB64 := base64.StdEncoding.EncodeToString(nonceBytes)

	respCh := ct.await(nonceB64)
	defer ct.cancel(nonceB64)

	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err := s.codeAttestor.SendCodeChallenge(sendCtx, deviceToken, env, pubKey, nonceB64)
	cancel()
	if err != nil {
		s.logger.Warn("code-attest push send failed", "provider_id", providerID, "error", err)
		return
	}

	select {
	case resp := <-respCh:
		if resp == nil || resp.Nonce != nonceB64 {
			s.logger.Warn("code-attest response nonce mismatch", "provider_id", providerID)
			return
		}
		// Verify Sign_SE(nonceB64) against the SE public key bound to THIS
		// connection at registration — never a key supplied in the response.
		if err := attestation.VerifyChallengeSignature(sePubKey, resp.Signature, nonceB64); err != nil {
			s.logger.Warn("code-attest signature verification failed", "provider_id", providerID, "error", err)
			return
		}
		provider.SetCodeAttested(true)
		s.logger.Info("provider code-attested via APNs", "provider_id", providerID)
		// Newly eligible for private routing — drain requests that queued waiting
		// for an attested provider instead of waiting for the next heartbeat tick.
		s.registry.DrainQueuedRequestsForProvider(provider)
	case <-time.After(CodeAttestResponseTimeout):
		s.logger.Warn("code-attest timed out awaiting WebSocket response", "provider_id", providerID)
	case <-ctx.Done():
	}
}
