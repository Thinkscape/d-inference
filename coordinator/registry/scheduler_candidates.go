package registry

import (
	"math"

	"github.com/eigeninference/d-inference/coordinator/protocol"
)

// buildCandidateWithReason returns the candidate plus, on rejection,
// the reason so callers can split metrics by failure mode.
func (r *Registry) buildCandidateWithReason(snap routingSnapshot, pr *PendingRequest) (*routingCandidate, candidateRejection, bool) {
	statePenalty, eligible := slotStatePenalty(snap.slotState)
	if !eligible {
		return nil, rejectNone, false
	}
	if !snap.hasHeadroom {
		return nil, rejectCapacity, false
	}

	if snap.systemMetrics.ThermalState == "critical" {
		return nil, rejectNone, false
	}

	reqMax := pr.RequestedMaxTokens
	if reqMax <= 0 {
		reqMax = defaultRequestedMaxTokens
	}
	reqPrompt := pr.EstimatedPromptTokens
	if reqPrompt < 0 {
		reqPrompt = 0
	}

	// Absolute hardware-fit gate (cold-load only, both admission modes). A model
	// whose footprint can never fit in this node's total memory must not be
	// routed here regardless of advertised token budget — otherwise the provider
	// 503s at load time ("Insufficient memory … need Y GB") and the request
	// bounces. This is the hole that let a 93.7 GB model get dispatched to 48/64
	// GB boxes: the token-budget admission path below never checked physical fit.
	//
	// Skip the gate whenever the model is already RESIDENT — a resident model has
	// demonstrably fit, so the heuristic must never reject it. The provider
	// reports "running" while actively serving and "idle" when loaded with no
	// in-flight requests (BatchScheduler+Telemetry: activeRequests>0 ? running :
	// idle); BOTH mean the weights are in GPU memory. `snap.modelLoaded` only
	// tracks "running", so we check the slot state directly here — otherwise an
	// idle-but-loaded provider would be wrongly excluded. Reported as
	// rejectModelTooLarge (permanent, not capacity).
	modelResident := slotStateModelLoaded(snap.slotState)
	if !modelResident && !modelFitsHardware(snap.minRAMGb, snap.modelSizeGB, snap.totalMemoryGB) {
		return nil, rejectModelTooLarge, false
	}

	// Free-memory admission gate (Phase 1). A provider that claims to
	// serve the model but doesn't have headroom for weights + KV cache
	// is rejected here so we don't OOM the backend post-routing.
	if !freeMemoryAdmits(snap, reqPrompt, reqMax) {
		return nil, rejectCapacity, false
	}

	effectiveQueue := snap.pendingForModel
	backendDepth := snap.backendRunning + snap.backendWaiting
	if backendDepth > effectiveQueue {
		effectiveQueue = backendDepth
	}

	waitingBacklogTokens := float64(snap.backendWaiting * reqMax)
	unaccountedPendingTokens := float64(snap.pendingMaxTokens) - float64(snap.maxTokensPotential) - waitingBacklogTokens
	if unaccountedPendingTokens < 0 {
		unaccountedPendingTokens = 0
	}

	effectiveTPS := resolveEffectiveTPS(snap)

	queueMs := float64(effectiveQueue) * queueDepthPenaltyMs
	pendingMs := float64(snap.totalPending) * totalPendingPenaltyMs
	var backlogMs float64
	if snap.activeTokenBudgetMax > 0 {
		tokensAhead := float64(snap.activeTokenBudgetUsed) + float64(snap.queuedTokenBudget)
		backlogMs = tokensAhead / effectiveTPS * 1000.0
	} else {
		backlogMs = backlogTokenMs(snap.maxTokensPotential, waitingBacklogTokens, unaccountedPendingTokens, effectiveTPS)
	}
	thisReqMs := float64(reqPrompt)/snap.prefillTPS*1000.0 + float64(reqMax)/effectiveTPS*1000.0
	healthMs := healthPenaltyMs(snap.systemMetrics, snap.gpuMemoryActiveGB, snap.totalMemoryGB)
	cost := statePenalty + queueMs + pendingMs + backlogMs + thisReqMs + healthMs

	return &routingCandidate{
		provider:       snap.provider,
		snapshot:       snap,
		costMs:         cost,
		effectiveQueue: effectiveQueue,
		effectiveTPS:   effectiveTPS,
		breakdown: costBreakdown{
			StateMs:   statePenalty,
			QueueMs:   queueMs,
			PendingMs: pendingMs,
			BacklogMs: backlogMs,
			ThisReqMs: thisReqMs,
			HealthMs:  healthMs,
			Total:     cost,
		},
	}, rejectNone, true
}

func slotStatePenalty(state string) (float64, bool) {
	switch state {
	case "", "running", "idle":
		return slotStatePenaltyRunning, true
	case "unknown":
		// Model is available but not loaded. The provider must evict the
		// current model and load this one — typically 15–60 seconds for
		// large models (depends on model size and disk speed). Warm
		// providers are strongly preferred but cold providers are still
		// eligible when no warm alternative exists.
		return slotStatePenaltyUnknown, true
	case "idle_shutdown":
		return slotStatePenaltyIdleShutdown, true
	case "reloading", "crashed":
		return math.Inf(1), false
	default:
		return slotStatePenaltyUnknown, true
	}
}

func slotStateModelLoaded(state string) bool {
	return state == "running" || state == "idle"
}

func backlogTokenMs(maxTokensPotential int64, waitingTokens, unaccountedPendingTokens, decodeTPS float64) float64 {
	if decodeTPS <= 0 {
		decodeTPS = 1.0
	}
	totalTokensAhead := float64(maxTokensPotential) + waitingTokens + unaccountedPendingTokens
	if totalTokensAhead < 0 {
		totalTokensAhead = 0
	}
	return totalTokensAhead / decodeTPS * 1000.0
}

func healthPenaltyMs(m protocol.SystemMetrics, gpuActiveGB, totalMemGB float64) float64 {
	penalty := m.MemoryPressure*memoryPressurePenaltyMs + m.CPUUsage*cpuUsagePenaltyMs
	switch m.ThermalState {
	case "fair":
		penalty += thermalPenaltyFairMs
	case "serious":
		penalty += thermalPenaltySeriousMs
	}
	if totalMemGB > 0 {
		gpuUtil := gpuActiveGB / totalMemGB
		if gpuUtil < 0 {
			gpuUtil = 0
		}
		if gpuUtil > 1 {
			gpuUtil = 1
		}
		penalty += gpuUtil * gpuUtilizationPenaltyMs
	}
	return penalty
}

// resolveEffectiveTPS returns the best available decode TPS estimate.
// Fallback chain: observed EWMA → fleet median → load-scaled benchmark.
func resolveEffectiveTPS(snap routingSnapshot) float64 {
	if snap.observedDecodeTPS > 0 {
		return snap.observedDecodeTPS
	}
	if snap.fleetMedianTPS > 0 {
		return snap.fleetMedianTPS
	}
	return effectiveDecodeTPS(snap.decodeTPS, snap.backendRunning)
}

// effectiveDecodeTPS scales the static decode TPS down by current
// backend batch size. Returns the static value when the load factor is
// disabled or batch is unknown. Floored at 1 token/s to avoid divide-
// by-zero.
//
// Note on the floor + large reqMax: when effectiveTPS bottoms out, the
// per-request decode cost (reqMax / effectiveTPS * 1000) can become
// very large for big reqMax values. This is intentional — a saturated
// provider should look strictly worse than less-saturated peers — and
// the maxConcurrency gate in snapshotProviderLocked already prevents
// us from getting here when batchSize exceeds the per-tier cap.
func effectiveDecodeTPS(staticTPS float64, backendRunning int) float64 {
	if staticTPS <= 0 {
		return 1.0
	}
	if effectiveTPSLoadFactor <= 0 || backendRunning <= 0 {
		return staticTPS
	}
	tps := staticTPS / (1.0 + effectiveTPSLoadFactor*float64(backendRunning))
	if tps < 1.0 {
		tps = 1.0
	}
	return tps
}

func resolvedDecodeTPS(p *Provider) float64 {
	if p.DecodeTPS > 0 {
		return p.DecodeTPS
	}
	bw := float64(p.Hardware.MemoryBandwidthGBs)
	if bw > 0 {
		return math.Sqrt(bw)
	}
	return 1.0
}

func resolvedPrefillTPS(p *Provider) float64 {
	if p.PrefillTPS > 0 {
		return p.PrefillTPS
	}
	return resolvedDecodeTPS(p) * 4.0
}

func providerModelIDs(p *Provider) []string {
	if p == nil {
		return nil
	}
	// p.Models is replaced (copy-on-write) by UpdateModelWeightHashes when a
	// challenge response carries refreshed weight hashes, so the slice header
	// must be read under p.mu. All callers invoke this helper after releasing
	// p.mu (verified: Heartbeat, RecordChallengeSuccess, SetProviderIdle,
	// DrainQueuedRequestsForProvider), so taking the lock here cannot deadlock.
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make([]string, 0, len(p.Models))
	for _, m := range p.Models {
		ids = append(ids, m.ID)
	}
	return ids
}
