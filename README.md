
# README.md
# KubeCostGuard

KubeCostGuard is a comprehensive Kubernetes cost optimization and health monitoring tool that helps you reduce cloud costs while maintaining cluster health and performance.

## Features

### 🏥 Health Monitoring
- **Cluster Health**: Monitor overall cluster health and performance
- **Node Monitoring**: Track node resource usage, conditions, and availability
- **Pod Monitoring**: Monitor pod status, restart counts, and resource consumption
- **Control Plane Health**: Keep track of control plane component health
- **Alerting**: Configurable alerts with multiple notification channels (Slack, Email, Webhook)

### 💰 Cost Management
- **Multi-Cloud Support**: AWS, GCP, and Azure cost tracking
- **Cost Analysis**: Detailed cost breakdown by service, namespace, and resource
- **Cost Recommendations**: Intelligent suggestions for cost reduction
- **Real-time Tracking**: Continuous cost monitoring and reporting

### ⚡ Optimization
- **Auto-scaling**: Intelligent horizontal and vertical pod autoscaling
- **Bin Packing**: Optimize pod placement for better resource utilization
- **Spot Instance Recommendations**: Identify opportunities to use spot instances
- **Rightsizing**: Recommendations for optimal resource allocation

## Quick Start

### Prerequisites
- Go 1.21+
- Kubernetes cluster
- kubectl configured
- Cloud provider credentials (AWS/GCP/Azure)

### Installation

1. **Clone the repository**
   ```bash
   git clone https://github.com/your-org/kubecostguard.git
   cd kubecostguard
   ```

2. **Build the application**
   ```bash
   make build
   ```

3. **Create configuration file**
   ```yaml
   # config.yaml
   server:
     port: 8080
     host: "0.0.0.0"
   
   health:
     interval: 30s
     alert_thresholds:
       cpu_threshold: 80.0
       memory_threshold: 85.0
   
   cost:
     providers: ["aws"]
     update_interval: 5m
   
   optimization:
     auto_scale: false
     bin_packing: true
     spot_recommendations: true
   ```

4. **Run the application**
   ```bash
   ./bin/kubecostguard -config=config.yaml
   ```

### Docker Deployment

1. **Build Docker image**
   ```bash
   make docker-build
   ```

2. **Deploy with Helm**
   ```bash
   make deploy-helm
   ```

### API Endpoints

- `GET /api/v1/health` - Application health check
- `GET /api/v1/health-monitoring/cluster` - Cluster health status
- `GET /api/v1/cost/analysis` - Cost analysis data
- `GET /api/v1/optimization/recommendations` - Optimization recommendations
- `GET /api/v1/metrics` - Prometheus metrics

## Configuration

### Environment Variables
- `KUBECONFIG` - Path to kubeconfig file
- `LOG_LEVEL` - Logging level (debug, info, warn, error)

### Cloud Provider Setup

#### AWS
```yaml
cost:
  aws:
    region: us-west-2
    access_key_id: your-access-key
    secret_access_key: your-secret-key
```

#### GCP
```yaml
cost:
  gcp:
    project_id: your-project-id
    service_account_path: /path/to/service-account.json
```

#### Azure
```yaml
cost:
  azure:
    subscription_id: your-subscription-id
    client_id: your-client-id
    client_secret: your-client-secret
    tenant_id: your-tenant-id
```

## Development

### Project Structure
```
kubecostguard/
├── cmd/kubecostguard/          # Application entry point
├── internal/                   # Private application code
│   ├── api/                   # REST API implementation
│   ├── config/                # Configuration management
│   ├── health/                # Health monitoring
│   ├── cost/                  # Cost management
│   ├── optimization/          # Optimization logic
│   └── kubernetes/            # Kubernetes client
├── pkg/                       # Public packages
│   ├── metrics/               # Metrics collection
│   └── utils/                 # Utility functions
└── deployments/               # Deployment manifests
```

### Building
```bash
# Build for current platform
make build

# Build for all platforms
make build-all

# Run tests
make test

# Run with coverage
make test-coverage
```

### Contributing
1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request

## Monitoring

### Prometheus Metrics
KubeCostGuard exposes various metrics for monitoring:
- `kubecostguard_node_health` - Node health status
- `kubecostguard_pod_health` - Pod health status
- `kubecostguard_cost_dollars` - Cost metrics by provider/service
- `kubecostguard_alerts_total` - Alert counters

### Grafana Dashboards
Pre-built Grafana dashboards are available in the `docs/grafana/` directory.

## License

This project is licensed under the MIT License - see the LICENSE file for details.
