package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RiskAssessment summarizes the most recent automated risk evaluation of a user.
//
// The fraud service writes it to PlatformAccess.spec.riskAssessment alongside
// spec.state, and the UserController copies it to User.status.riskAssessment so
// that listing Users carries each user's latest score and decision without the
// caller having to read the full evaluation records. Values are free-form strings
// because milo does not own, and must not import, the fraud service's types.
type RiskAssessment struct {
	// Score is the composite risk score as a decimal string, for example "0.12".
	// +optional
	Score string `json:"score,omitempty"`

	// Decision is the evaluation outcome, for example ACCEPTED, REVIEW, DEACTIVATE or REJECTED.
	// +optional
	Decision string `json:"decision,omitempty"`

	// EvaluatedAt is when the evaluation completed.
	// +optional
	EvaluatedAt *metav1.Time `json:"evaluatedAt,omitempty"`

	// EvaluationName is the name of the evaluation this summary was taken from.
	// +optional
	EvaluationName string `json:"evaluationName,omitempty"`
}
