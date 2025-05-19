package recommender

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// RecommendationsCollector aggregates recommendations from all recommenders
// Recommendation defines the interface that all recommendation types must implement

type RecommendationsCollector struct {
	recommendations []Recommendation
	mu              sync.RWMutex
	history         []RecommendationHistory
	historyMu       sync.RWMutex
}

// RecommendationHistory stores applied recommendations
type RecommendationHistory struct {
	ID            string
	Type          string
	AppliedAt     time.Time
	OriginalState map[string]interface{}
	Result        map[string]interface{}
	ActualSavings float64
	Error         error
}

// NewRecommendationsCollector creates a new recommendations collector
func NewRecommendationsCollector() *RecommendationsCollector {
	return &RecommendationsCollector{
		recommendations: make([]Recommendation, 0),
		history:         make([]RecommendationHistory, 0),
	}
}

// AddRecommendations adds recommendations from a recommender
func (c *RecommendationsCollector) AddRecommendations(recommenderName string, recommendations interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Convert to Recommendation interface if possible
	switch recs := recommendations.(type) {
	case []*RightsizingRecommendation:
		for _, rec := range recs {
			c.recommendations = append(c.recommendations, rec)
		}
	case []*IdleResourceRecommendation:
		for _, rec := range recs {
			c.recommendations = append(c.recommendations, rec)
		}
		// Add more cases as needed for different recommendation types
	}
}

// GetAllRecommendations returns all collected recommendations
func (c *RecommendationsCollector) GetAllRecommendations() []Recommendation {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]Recommendation, len(c.recommendations))
	copy(result, c.recommendations)

	// Sort by potential savings
	sort.Slice(result, func(i, j int) bool {
		return result[i].GetPotentialSavings() > result[j].GetPotentialSavings()
	})

	return result
}

// GetTopRecommendations returns the top N recommendations by savings
func (c *RecommendationsCollector) GetTopRecommendations(n int) []Recommendation {
	recommendations := c.GetAllRecommendations()

	if n > len(recommendations) {
		n = len(recommendations)
	}

	return recommendations[:n]
}

// GetRecommendationsByType returns recommendations of a specific type
func (c *RecommendationsCollector) GetRecommendationsByType(recType string) []Recommendation {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]Recommendation, 0)
	for _, rec := range c.recommendations {
		if rec.GetType() == recType {
			result = append(result, rec)
		}
	}

	return result
}

// GetRecommendationsByPriority returns recommendations filtered by priority
func (c *RecommendationsCollector) GetRecommendationsByPriority(minPriority int) []Recommendation {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]Recommendation, 0)
	for _, rec := range c.recommendations {
		if rec.GetPriority() >= minPriority {
			result = append(result, rec)
		}
	}

	return result
}

// ApplyRecommendation applies a specific recommendation by ID
func (c *RecommendationsCollector) ApplyRecommendation(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, rec := range c.recommendations {
		if rec.GetID() == id {
			// Store current state for history
			history := RecommendationHistory{
				ID:            id,
				Type:          rec.GetType(),
				AppliedAt:     time.Now(),
				OriginalState: rec.GetDetails(),
			}

			// Apply the recommendation
			err := rec.Apply()
			if err != nil {
				history.Error = err
			} else {
				// Simulate to get result
				result, err := rec.Simulate()
				if err == nil {
					history.Result = result
					if savings, ok := result["savings"].(float64); ok {
						history.ActualSavings = savings
					}
				}
			}

			// Store in history
			c.addToHistory(history)

			return err
		}
	}

	return fmt.Errorf("recommendation not found: %s", id)
}

// addToHistory adds a recommendation application to history
func (c *RecommendationsCollector) addToHistory(history RecommendationHistory) {
	c.historyMu.Lock()
	defer c.historyMu.Unlock()

	c.history = append(c.history, history)

	// Keep only last 1000 entries
	if len(c.history) > 1000 {
		c.history = c.history[len(c.history)-1000:]
	}
}

// GetHistory returns the application history
func (c *RecommendationsCollector) GetHistory(limit int) []RecommendationHistory {
	c.historyMu.RLock()
	defer c.historyMu.RUnlock()

	if limit <= 0 || limit > len(c.history) {
		limit = len(c.history)
	}

	result := make([]RecommendationHistory, limit)
	// Return most recent first
	for i := 0; i < limit; i++ {
		result[i] = c.history[len(c.history)-1-i]
	}

	return result
}

// GetTotalPotentialSavings calculates total potential savings from all recommendations
func (c *RecommendationsCollector) GetTotalPotentialSavings() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var total float64
	for _, rec := range c.recommendations {
		total += rec.GetPotentialSavings()
	}

	return total
}

// Clear removes all recommendations
func (c *RecommendationsCollector) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.recommendations = make([]Recommendation, 0)
}

// GetSummary returns a summary of all recommendations
func (c *RecommendationsCollector) GetSummary() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	summary := make(map[string]interface{})

	// Count by type
	countByType := make(map[string]int)
	totalSavings := float64(0)

	for _, rec := range c.recommendations {
		countByType[rec.GetType()]++
		totalSavings += rec.GetPotentialSavings()
	}

	summary["total_recommendations"] = len(c.recommendations)
	summary["total_potential_savings"] = totalSavings
	summary["recommendations_by_type"] = countByType
	summary["last_updated"] = time.Now()

	return summary
}
