package recommender

import "time"

// Recommendation interface provides common methods for all recommendation types
type Recommendation interface {
	GetID() string
	GetType() string
	GetResourceName() string
	GetPotentialSavings() float64
	GetPriority() int
	GetConfidence() float64
	GetDescription() string
	GetDetails() map[string]interface{}
	GetCreatedAt() time.Time

	// Apply applies the recommendation
	Apply() error

	// Simulate simulates the impact of applying the recommendation
	Simulate() (map[string]interface{}, error)
}
