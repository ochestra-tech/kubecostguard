// 2. Core Libraries and Dependencies

// File: internal/config/config.go
package config

import (
	"fmt"
	"io/ioutil"

	yaml "gopkg.in/yaml.v3"
)

// Config represents the application configuration
type Config struct {
	Kubernetes   KubernetesConfig   `yaml:"kubernetes"`
	Health       HealthConfig       `yaml:"health"`
	Cost         CostConfig         `yaml:"cost"`
	Optimization OptimizationConfig `yaml:"optimization"`
	API          APIConfig          `yaml:"api"`
}

// KubernetesConfig holds Kubernetes connection settings
type KubernetesConfig struct {
	Kubeconfig string `yaml:"kubeconfig"`
	InCluster  bool   `yaml:"inCluster"`
}

// HealthConfig holds health monitoring configuration
type HealthConfig struct {
	ScrapeIntervalSeconds int      `yaml:"scrapeIntervalSeconds"`
	EnabledCollectors     []string `yaml:"enabledCollectors"`
	AlertThresholds       struct {
		CPUUtilizationPercent    float64 `yaml:"cpuUtilizationPercent"`
		MemoryUtilizationPercent float64 `yaml:"memoryUtilizationPercent"`
		PodRestarts              int     `yaml:"podRestarts"`
	} `yaml:"alertThresholds"`
}

// CostConfig holds cost analysis configuration
type CostConfig struct {
	UpdateIntervalMinutes int    `yaml:"updateIntervalMinutes"`
	CloudProvider         string `yaml:"cloudProvider"`
	PricingAPIEndpoint    string `yaml:"pricingApiEndpoint"`
	StorageBackend        string `yaml:"storageBackend"`
	StoragePath           string `yaml:"storagePath"`
}

// OptimizationConfig holds optimization configuration
type OptimizationConfig struct {
	EnableAutoScaling     bool    `yaml:"enableAutoScaling"`
	IdleResourceThreshold float64 `yaml:"idleResourceThreshold"`
	RightsizingThreshold  float64 `yaml:"rightsizingThreshold"`
	EnableSpotRecommender bool    `yaml:"enableSpotRecommender"`
	MinimumSavingsPercent float64 `yaml:"minimumSavingsPercent"`
	OptimizationInterval  int     `yaml:"optimizationIntervalHours"`
	ApplyRecommendations  bool    `yaml:"applyRecommendations"`
	DryRun                bool    `yaml:"dryRun"`
}

// APIConfig holds API server configuration
type APIConfig struct {
	Port           int    `yaml:"port"`
	TLSEnabled     bool   `yaml:"tlsEnabled"`
	TLSCertPath    string `yaml:"tlsCertPath"`
	TLSKeyPath     string `yaml:"tlsKeyPath"`
	Authentication struct {
		Enabled bool   `yaml:"enabled"`
		Type    string `yaml:"type"`
		JWTKey  string `yaml:"jwtKey"`
	} `yaml:"authentication"`
}

// setDefaults initializes configuration with default values
func setDefaults(config *Config) {
	config.Health.ScrapeIntervalSeconds = 60
	config.Cost.UpdateIntervalMinutes = 30
	config.API.Port = 8080
	config.Optimization.IdleResourceThreshold = 0.2
	config.Optimization.MinimumSavingsPercent = 20.0
	config.Optimization.OptimizationInterval = 24
}

// Load reads configuration from file and environment
func Load(configPath string) (*Config, error) {
	config := &Config{}

	// Set defaults
	setDefaults(config)

	// Load from file if provided
	if configPath != "" {
		data, err := ioutil.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}

		if err := yaml.Unmarshal(data, config); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
	}

	// Override with environment variables
	// TODO: Implement environment variable overrides if needed
	// applyEnvironment(config)

	// Validate configuration
	if err := validate(config); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return config, nil
}

// validate checks if the configuration is valid
func validate(config *Config) error {
	if config.Health.ScrapeIntervalSeconds <= 0 {
		return fmt.Errorf("scrape interval must be positive")
	}
	if config.Cost.UpdateIntervalMinutes <= 0 {
		return fmt.Errorf("update interval must be positive")
	}
	if config.API.Port <= 0 {
		return fmt.Errorf("API port must be positive")
	}
	return nil
}
