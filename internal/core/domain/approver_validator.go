package domain

import "errors"

// ErrAIAgentCannotApproveAIAgent is returned by ValidateApproverPermitted
// when both the original requester and the approver are AI agents.
// Pins the structural rule of ADR 0035 (refs#0011) at the domain layer.
var ErrAIAgentCannotApproveAIAgent = errors.New("ai agent cannot approve ai agent")

// ErrInitiatingActorRequiredForDaemon is returned when ADR 0037 §Axis 3
// REQUIRED constraint is violated: a HIGH severity approval flow has
// RequesterActorType=CallerWorkspaceDaemon but InitiatingActorType is
// empty, leaving the laundering path open.
var ErrInitiatingActorRequiredForDaemon = errors.New("initiating actor type required when requester is workspace-daemon")

// EffectiveRequesterActorType implements ADR 0037's daemon hop rule:
// when RequesterActorType is CallerWorkspaceDaemon, the effective
// requester for AI-vs-AI determination is the InitiatingActorType
// (= the distal actor that scheduled the daemon's action). Otherwise
// the proximate RequesterActorType passes through unchanged.
//
// Returns ErrInitiatingActorRequiredForDaemon when RequesterActorType
// is CallerWorkspaceDaemon AND InitiatingActorType is empty. Callers
// in HIGH severity paths SHALL fail-closed on this sentinel; non-HIGH
// callers MAY tolerate empty InitiatingActorType per ADR 0037 §Axis 3
// (mandatory boundary aligns with ADR 0036 HIGH scope).
func EffectiveRequesterActorType(req ApprovalRequest) (CallerType, error) {
	if req.RequesterActorType == CallerWorkspaceDaemon {
		if req.InitiatingActorType == "" {
			return "", ErrInitiatingActorRequiredForDaemon
		}
		return req.InitiatingActorType, nil
	}
	return req.RequesterActorType, nil
}

// ActorTypeSource is the metadata-input enum producers may write into
// the requester_actor_source DMail metadata key (per ADR 0037 §Axis 4).
// The enum is closed: { ActorSourceBroker, ActorSourceEnv, ActorSourceUnknown }.
// Producers SHOULD NOT write ActorSourceBroker — only the gateway-internal
// emit path attaches it. A producer writing ActorSourceBroker is reclassified
// as spoof at the gateway (= GatewayClassificationSpoofedBroker).
type ActorTypeSource string

const (
	// ActorSourceBroker indicates the value originated from a broker
	// token claim. Producer-emitted values of this kind are spoofs;
	// only the gateway should attach this source.
	ActorSourceBroker ActorTypeSource = "broker"
	// ActorSourceEnv indicates the producer set the value from
	// RUNOPS_ACTOR_TYPE env var.
	ActorSourceEnv ActorTypeSource = "env"
	// ActorSourceUnknown indicates the producer had no source. The
	// canonical value when the metadata key is absent or empty.
	ActorSourceUnknown ActorTypeSource = "unknown"
)

// GatewayClassification is the gateway-internal classification of an
// inbound DMail's requester_actor_source value, derived from the
// metadata input enum plus the gateway's own request context (per
// ADR 0037 §Axis 4 two-enum split).
type GatewayClassification string

const (
	// GatewayClassificationBrokerVerified indicates the gateway-internal
	// emit path attached the value using its own authenticated broker
	// context. Trusted for security policy.
	GatewayClassificationBrokerVerified GatewayClassification = "broker_verified"
	// GatewayClassificationEnvAttested indicates a producer set the value
	// from RUNOPS_ACTOR_TYPE env. Self-attested but acknowledged.
	GatewayClassificationEnvAttested GatewayClassification = "env_attested"
	// GatewayClassificationUnknown indicates the producer had no source.
	// HIGH severity gates fail-closed on this classification.
	GatewayClassificationUnknown GatewayClassification = "unknown"
	// GatewayClassificationSpoofedBroker indicates a producer wrote
	// ActorSourceBroker without a gateway-attached broker context. The
	// gateway treats this as a spoof attempt; HIGH severity gates
	// fail-closed immediately and audit log the event.
	GatewayClassificationSpoofedBroker GatewayClassification = "spoofed_broker"
)

// ClassifyRequesterActorSource maps the producer-input enum (raw
// requester_actor_source DMail metadata value) plus the gateway-known
// flag "is this DMail from a path the gateway authenticated as broker?"
// to the gateway-internal classification (per ADR 0037 §Axis 4).
//
// gatewayAttachedBroker is true only when the inbound DMail was emitted
// by the gateway's own broker-aware emit path (= cmd/server-side).
// External producers MUST be passed gatewayAttachedBroker=false.
func ClassifyRequesterActorSource(rawSource string, gatewayAttachedBroker bool) GatewayClassification {
	switch ActorTypeSource(rawSource) {
	case ActorSourceBroker:
		if gatewayAttachedBroker {
			return GatewayClassificationBrokerVerified
		}
		return GatewayClassificationSpoofedBroker
	case ActorSourceEnv:
		return GatewayClassificationEnvAttested
	default:
		return GatewayClassificationUnknown
	}
}

// ValidateApproverPermitted enforces the ADR 0035 invariant: an AI agent
// cannot approve another AI agent's convergence request. Empty CallerType
// (zero value) is treated as CallerHumanOperator to preserve backwards
// compatibility with construction sites that pre-date RequesterActorType.
//
// All other combinations (human-vs-AI, gateway-vs-AI, etc.) are permitted
// at the domain layer; further restrictions are layered on by the
// use-case (auth.IsAuthorized) and the Slack inbound adapter
// (SLACK_AI_AGENT_BOT_USER_IDS classification).
func ValidateApproverPermitted(req ApprovalRequest, approverType CallerType) error {
	requester := req.RequesterActorType
	if requester == "" {
		requester = CallerHumanOperator
	}
	if approverType == "" {
		approverType = CallerHumanOperator
	}
	if requester == CallerAIAgent && approverType == CallerAIAgent {
		return ErrAIAgentCannotApproveAIAgent
	}
	return nil
}
