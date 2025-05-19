package recommender

// Recommender is the interface for all recommendation generators
type Recommender interface {
	// Name returns the name of the recommender
	Name() string

	// GenerateRecommendations generates cost optimization recommendations
	GenerateRecommendations(resources map[string]interface{}, costs map[string]float64) (interface{}, error)
}
