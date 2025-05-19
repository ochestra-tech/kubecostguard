// AWS Cost Provider for KubecostGuard
// This file contains the implementation of the AWS cost provider for KubecostGuard.
// It retrieves cost information for AWS resources in a Kubernetes cluster.
// The provider uses AWS SDK to interact with EC2 and Pricing APIs to fetch instance pricing.
// It also includes methods for estimating cost savings from optimization recommendations.
// The code is structured to allow easy extension for other cloud providers in the future.
// The code is organized into a package named "providers" and includes necessary imports.
// The main functionality is encapsulated in the AWSCostProvider struct, which implements
// the CostProvider interface. The provider retrieves costs for AWS resources, including
package providers

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/pricing"
	"github.com/aws/aws-sdk-go/service/sts"
)

// AWSCostProvider implements cost retrieval for AWS resources
type AWSCostProvider struct {
	session       *session.Session
	ec2Client     *ec2.EC2
	pricingClient *pricing.Pricing
	region        string
	cache         map[string]float64
	cacheExpiry   time.Time
	cacheDuration time.Duration
}

// AWSResourceCost represents the cost structure for AWS resources
type AWSResourceCost struct {
	ResourceID   string
	ResourceType string
	InstanceType string
	HourlyRate   float64
	MonthlyRate  float64
	Region       string
	AZ           string
	Tags         map[string]string
}

// NewAWSCostProvider creates a new AWS cost provider
func NewAWSCostProvider(endpoint string) *AWSCostProvider {
	// Create AWS session
	sess, err := session.NewSession()
	if err != nil {
		log.Fatalf("Failed to create AWS session: %v", err)
	}

	// Get region from session config or default to us-east-1
	region := "us-east-1"
	if sess.Config.Region != nil && *sess.Config.Region != "" {
		region = *sess.Config.Region
	}

	// Pricing API is only available in us-east-1
	pricingSess := sess.Copy(&aws.Config{Region: aws.String("us-east-1")})

	return &AWSCostProvider{
		session:       sess,
		ec2Client:     ec2.New(sess),
		pricingClient: pricing.New(pricingSess),
		region:        region,
		cache:         make(map[string]float64),
		cacheExpiry:   time.Now(),
		cacheDuration: 24 * time.Hour, // Cache pricing data for 24 hours
	}
}

// GetResourceCosts retrieves costs for resources in the cluster
func (p *AWSCostProvider) GetResourceCosts(resources map[string]interface{}) (map[string]float64, error) {
	costs := make(map[string]float64)

	// Extract nodes from resources
	nodes, ok := resources["nodes"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("failed to extract nodes from resources")
	}

	// Get instance details for each node
	for _, node := range nodes {
		nodeMap, ok := node.(map[string]interface{})
		if !ok {
			continue
		}

		nodeName, ok := nodeMap["metadata"].(map[string]interface{})["name"].(string)
		if !ok {
			continue
		}

		// Extract instance ID from node metadata or annotations
		instanceID := p.extractInstanceID(nodeMap)
		if instanceID == "" {
			continue
		}

		// Get instance details and cost
		cost, err := p.getInstanceCost(instanceID)
		if err != nil {
			log.Printf("Failed to get cost for instance %s: %v", instanceID, err)
			continue
		}

		costs[nodeName] = cost
	}

	return costs, nil
}

// extractInstanceID extracts AWS instance ID from node metadata
func (p *AWSCostProvider) extractInstanceID(nodeMap map[string]interface{}) string {
	// Try to get provider ID first
	if status, ok := nodeMap["status"].(map[string]interface{}); ok {
		if nodeInfo, ok := status["nodeInfo"].(map[string]interface{}); ok {
			if providerID, ok := nodeInfo["machineID"].(string); ok {
				// Provider ID format: aws:///availability-zone/instance-id
				parts := strings.Split(providerID, "/")
				if len(parts) > 0 {
					return parts[len(parts)-1]
				}
			}
		}
	}

	// Try to get from annotations
	if metadata, ok := nodeMap["metadata"].(map[string]interface{}); ok {
		if annotations, ok := metadata["annotations"].(map[string]string); ok {
			if instanceID, ok := annotations["node.alpha.kubernetes.io/instance-id"]; ok {
				return instanceID
			}
		}
	}

	return ""
}

// getInstanceCost retrieves the cost for a specific instance
func (p *AWSCostProvider) getInstanceCost(instanceID string) (float64, error) {
	// First, get instance details
	input := &ec2.DescribeInstancesInput{
		InstanceIds: []*string{aws.String(instanceID)},
	}

	result, err := p.ec2Client.DescribeInstances(input)
	if err != nil {
		return 0, fmt.Errorf("failed to describe instance: %w", err)
	}

	if len(result.Reservations) == 0 || len(result.Reservations[0].Instances) == 0 {
		return 0, fmt.Errorf("instance not found")
	}

	instance := result.Reservations[0].Instances[0]
	instanceType := *instance.InstanceType

	// Check cache first
	cacheKey := fmt.Sprintf("%s-%s", instanceType, p.region)
	if p.cacheExpiry.After(time.Now()) {
		if cachedPrice, ok := p.cache[cacheKey]; ok {
			return cachedPrice, nil
		}
	}

	// Get pricing for the instance type
	hourlyRate, err := p.getInstancePricing(instanceType)
	if err != nil {
		return 0, fmt.Errorf("failed to get pricing: %w", err)
	}

	// Cache the result
	p.cache[cacheKey] = hourlyRate
	p.cacheExpiry = time.Now().Add(p.cacheDuration)

	// Calculate monthly rate
	monthlyRate := hourlyRate * 24 * 30

	return monthlyRate, nil
}

// getInstancePricing retrieves pricing information for an instance type
func (p *AWSCostProvider) getInstancePricing(instanceType string) (float64, error) {
	// Prepare filter
	filters := []*pricing.Filter{
		{
			Type:  aws.String("TERM_MATCH"),
			Field: aws.String("instanceType"),
			Value: aws.String(instanceType),
		},
		{
			Type:  aws.String("TERM_MATCH"),
			Field: aws.String("location"),
			Value: aws.String(p.getRegionDescription()),
		},
		{
			Type:  aws.String("TERM_MATCH"),
			Field: aws.String("operatingSystem"),
			Value: aws.String("Linux"),
		},
		{
			Type:  aws.String("TERM_MATCH"),
			Field: aws.String("preInstalledSw"),
			Value: aws.String("NA"),
		},
		{
			Type:  aws.String("TERM_MATCH"),
			Field: aws.String("tenancy"),
			Value: aws.String("Shared"),
		},
	}

	// Get products
	input := &pricing.GetProductsInput{
		ServiceCode: aws.String("AmazonEC2"),
		Filters:     filters,
	}

	products, err := p.pricingClient.GetProducts(input)
	if err != nil {
		return 0, fmt.Errorf("failed to get products: %w", err)
	}

	if len(products.PriceList) == 0 {
		return 0, fmt.Errorf("no pricing found for instance type %s", instanceType)
	}

	// Parse pricing from the first product
	priceJSON := products.PriceList[0]
	hourlyPrice, err := p.parsePriceJSON(priceJSON)
	if err != nil {
		return 0, fmt.Errorf("failed to parse price: %w", err)
	}

	return hourlyPrice, nil
}

// parsePriceJSON extracts the hourly price from AWS pricing JSON
func (p *AWSCostProvider) parsePriceJSON(priceJSON string) (float64, error) {
	// This is a simplified parser. In production, you'd use a JSON parser
	// to properly extract the on-demand pricing

	// Look for the on-demand pricing section
	if !strings.Contains(priceJSON, "OnDemand") {
		return 0, fmt.Errorf("on-demand pricing not found")
	}

	// Extract price (this is a simplified approach)
	startIdx := strings.Index(priceJSON, "\"unit\":\"Hrs\"") - 100
	if startIdx < 0 {
		startIdx = 0
	}
	endIdx := strings.Index(priceJSON[startIdx:], "\"unit\":\"Hrs\"") + 50
	if endIdx > len(priceJSON) {
		endIdx = len(priceJSON)
	}

	section := priceJSON[startIdx:endIdx]

	// Find USD price value
	usdIndex := strings.Index(section, "\"USD\":\"")
	if usdIndex == -1 {
		return 0, fmt.Errorf("USD price not found")
	}

	// Extract the price value
	priceStart := usdIndex + 7
	priceEnd := strings.Index(section[priceStart:], "\"")
	if priceEnd == -1 {
		return 0, fmt.Errorf("price value end not found")
	}

	priceStr := section[priceStart : priceStart+priceEnd]

	var price float64
	_, err := fmt.Sscanf(priceStr, "%f", &price)
	if err != nil {
		return 0, fmt.Errorf("failed to parse price: %w", err)
	}

	return price, nil
}

// getRegionDescription returns the full region description for pricing API
func (p *AWSCostProvider) getRegionDescription() string {
	regionDescriptions := map[string]string{
		"us-east-1":      "US East (N. Virginia)",
		"us-east-2":      "US East (Ohio)",
		"us-west-1":      "US West (N. California)",
		"us-west-2":      "US West (Oregon)",
		"eu-west-1":      "EU (Ireland)",
		"eu-west-2":      "EU (London)",
		"eu-west-3":      "EU (Paris)",
		"eu-central-1":   "EU (Frankfurt)",
		"ap-southeast-1": "Asia Pacific (Singapore)",
		"ap-southeast-2": "Asia Pacific (Sydney)",
		"ap-northeast-1": "Asia Pacific (Tokyo)",
		"ap-northeast-2": "Asia Pacific (Seoul)",
		"ap-south-1":     "Asia Pacific (Mumbai)",
		"ca-central-1":   "Canada (Central)",
		"sa-east-1":      "South America (Sao Paulo)",
	}

	if desc, ok := regionDescriptions[p.region]; ok {
		return desc
	}
	return p.region
}

// EstimateOptimizationSavings estimates cost savings for optimization recommendations
func (p *AWSCostProvider) EstimateOptimizationSavings(optimization map[string]interface{}) (float64, error) {
	optimizationType, ok := optimization["type"].(string)
	if !ok {
		return 0, fmt.Errorf("optimization type not found")
	}

	switch optimizationType {
	case "rightsizing":
		return p.estimateRightsizingSavings(optimization)
	case "spot_instance":
		return p.estimateSpotSavings(optimization)
	case "reserved_instance":
		return p.estimateReservedInstanceSavings(optimization)
	default:
		return 0, fmt.Errorf("unknown optimization type: %s", optimizationType)
	}
}

// estimateRightsizingSavings calculates savings from instance rightsizing
func (p *AWSCostProvider) estimateRightsizingSavings(optimization map[string]interface{}) (float64, error) {
	currentType, ok := optimization["current_instance_type"].(string)
	if !ok {
		return 0, fmt.Errorf("current instance type not specified")
	}

	recommendedType, ok := optimization["recommended_instance_type"].(string)
	if !ok {
		return 0, fmt.Errorf("recommended instance type not specified")
	}

	// Get current cost
	currentCost, err := p.getInstancePricing(currentType)
	if err != nil {
		return 0, fmt.Errorf("failed to get current instance pricing: %w", err)
	}

	// Get recommended cost
	recommendedCost, err := p.getInstancePricing(recommendedType)
	if err != nil {
		return 0, fmt.Errorf("failed to get recommended instance pricing: %w", err)
	}

	// Calculate monthly savings
	monthlySavings := (currentCost - recommendedCost) * 24 * 30
	return monthlySavings, nil
}

// estimateSpotSavings calculates savings from using spot instances
func (p *AWSCostProvider) estimateSpotSavings(optimization map[string]interface{}) (float64, error) {
	// Spot instances typically save 60-70% compared to on-demand
	spotSavingsRate := 0.65 // 65% savings

	onDemandCost, ok := optimization["current_monthly_cost"].(float64)
	if !ok {
		return 0, fmt.Errorf("current monthly cost not specified")
	}

	spotCost := onDemandCost * (1 - spotSavingsRate)
	monthlySavings := onDemandCost - spotCost

	return monthlySavings, nil
}

// estimateReservedInstanceSavings calculates savings from reserved instances
func (p *AWSCostProvider) estimateReservedInstanceSavings(optimization map[string]interface{}) (float64, error) {
	// Reserved instances typically save 30-60% depending on term
	term, ok := optimization["reserved_term"].(string)
	if !ok {
		term = "1year" // Default to 1 year
	}

	var savingsRate float64
	switch term {
	case "1year":
		savingsRate = 0.30 // 30% savings
	case "3year":
		savingsRate = 0.45 // 45% savings
	default:
		savingsRate = 0.30
	}

	onDemandCost, ok := optimization["current_monthly_cost"].(float64)
	if !ok {
		return 0, fmt.Errorf("current monthly cost not specified")
	}

	reservedCost := onDemandCost * (1 - savingsRate)
	monthlySavings := onDemandCost - reservedCost

	return monthlySavings, nil
}

// ValidateCredentials validates AWS credentials
func (p *AWSCostProvider) ValidateCredentials() error {
	stsClient := sts.New(p.session)
	_, err := stsClient.GetCallerIdentity(&sts.GetCallerIdentityInput{})
	if err != nil {
		return fmt.Errorf("failed to validate AWS credentials: %w", err)
	}
	return nil
}
