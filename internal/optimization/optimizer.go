// 5. Optimization Implementation

// File: internal/optimization/optimizer.go
package optimization

import (
	"context"
	"log"
	"time"

	"github.com/ochestra-tech/kubecostguard/internal/config"
	"github.com/ochestra-tech/kubecostguard/internal/cost"
	"github.com/ochestra-tech/kubecostguard/internal/kubernetes"
)

// Optimizer manages resource optimization in the cluster
type Optimizer struct {
	ctx             context.Context
	k8sClient       *kubernetes.Client
	costAnalyzer    *cost.Analyzer
	config          config.OptimizationConfig
	scaler          *Scaler
	binPacker       *BinPacker
	spotRecommender *SpotRecommender
	stopChan        chan struct{}
}

// NewOptimizer creates a new optimizer
func NewOptimizer(ctx context.Context, k8sClient *kubernetes.Client, costAnalyzer *cost.Analyzer, config config.OptimizationConfig) *Optimizer {
	o := &Optimizer{
		ctx:          ctx,
		k8sClient:    k8sClient,
		costAnalyzer: costAnalyzer,
		config:       config,
		stopChan:     make(chan struct{}),
	}

	// Initialize optimization components
	o.scaler = NewScaler(k8sClient, config)
	o.binPacker = NewBinPacker(k8sClient, config)
	o.spotRecommender = NewSpotRecommender(k8sClient, costAnalyzer, config)

	return o
}

// Start begins the optimization process
func (o *Optimizer) Start() error {
	// Start optimization loop
	go o.optimizationLoop()

	return nil
}

// optimizationLoop periodically runs optimization
func (o *Optimizer) optimizationLoop() {
	ticker := time.NewTicker(time.Duration(o.config.OptimizationInterval) * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			o.runOptimizations()
		case <-o.stopChan:
			return
		case <-o.ctx.Done():
			return
		}
	}
}

// runOptimizations executes the optimization process
func (o *Optimizer) runOptimizations() {
	log.Println("Running optimization cycle...")

	// Get current cost data
	costData := o.costAnalyzer.GetCostData()

	// Run auto-scaling if enabled
	if o.config.EnableAutoScaling {
		recommendations, err := o.scaler.GenerateRecommendations(costData)
		if err != nil {
			log.Printf("Error generating scaling recommendations: %v", err)
		} else {
			log.Printf("Generated %d scaling recommendations", len(recommendations))

			if o.config.ApplyRecommendations && !o.config.DryRun {
				applied, err := o.scaler.ApplyRecommendations(recommendations)
				if err != nil {
					log.Printf("Error applying scaling recommendations: %v", err)
				} else {
					log.Printf("Applied %d scaling recommendations", applied)
				}
			}
		}
	}

	// Run bin-packing optimization
	binPackingRecs, err := o.binPacker.GenerateRecommendations(costData)
	if err != nil {
		log.Printf("Error generating bin-packing recommendations: %v", err)
	} else {
		log.Printf("Generated %d bin-packing recommendations", len(binPackingRecs))

		if o.config.ApplyRecommendations && !o.config.DryRun {
			applied, err := o.binPacker.ApplyRecommendations(binPackingRecs)
			if err != nil {
				log.Printf("Error applying bin-packing recommendations: %v", err)
			} else {
				log.Printf("Applied %d bin-packing recommendations", applied)
			}
		}
	}

	// Run spot instance recommendations if enabled
	if o.config.EnableSpotRecommender {
		spotRecs, err := o.spotRecommender.GenerateRecommendations(costData)
		if err != nil {
			log.Printf("Error generating spot instance recommendations: %v", err)
		} else {
			log.Printf("Generated %d spot instance recommendations", len(spotRecs))

			if o.config.ApplyRecommendations && !o.config.DryRun {
				applied, err := o.spotRecommender.ApplyRecommendations(spotRecs)
				if err != nil {
					log.Printf("Error applying spot instance recommendations: %v", err)
				} else {
					log.Printf("Applied %d spot instance recommendations", applied)
				}
			}
		}
	}

	log.Println("Optimization cycle completed")
}

// GetOptimizationSummary returns a summary of optimization recommendations
func (o *Optimizer) GetOptimizationSummary() (map[string]interface{}, error) {
	summary := make(map[string]interface{})

	// Get current cost data
	costData := o.costAnalyzer.GetCostData()

	// Get scaling recommendations
	if o.config.EnableAutoScaling {
		scalingRecs, err := o.scaler.GenerateRecommendations(costData)
		if err != nil {
			log.Printf("Error generating scaling recommendations: %v", err)
		} else {
			summary["scaling_recommendations"] = scalingRecs
		}
	}

	// Get bin-packing recommendations
	binPackingRecs, err := o.binPacker.GenerateRecommendations(costData)
	if err != nil {
		log.Printf("Error generating bin-packing recommendations: %v", err)
	} else {
		summary["binpacking_recommendations"] = binPackingRecs
	}

	// Get spot instance recommendations
	if o.config.EnableSpotRecommender {
		spotRecs, err := o.spotRecommender.GenerateRecommendations(costData)
		if err != nil {
			log.Printf("Error generating spot instance recommendations: %v", err)
		} else {
			summary["spot_recommendations"] = spotRecs
		}
	}

	return summary, nil
}

// Stop halts the optimization process
func (o *Optimizer) Stop() {
	close(o.stopChan)
}
