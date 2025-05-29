# Configuration Guide

## Configuration File Structure

KubeCostGuard uses a YAML configuration file with the following structure:

### Server Configuration

```yaml
server:
  port: 8080              # API server port
  host: "0.0.0.0"         # Bind address
  tls_cert: ""            # Optional TLS certificate path
  tls_key: ""             # Optional TLS key path
```

### Kubernetes Configuration

```yaml
kubernetes:
  in_cluster: true        # Use in-cluster configuration
  config_path: ""         # Path to kubeconfig (when in_cluster=false)
  namespace: ""           # Namespace to monitor (empty = all)
```

### Health Monitoring Configuration

```yaml
health:
  interval: 30s           # Health check interval
  alert_thresholds:
    cpu_threshold: 80.0   # CPU alert threshold (%)
    memory_threshold: 85.0 # Memory alert threshold (%)
    disk_threshold: 90.0  # Disk alert threshold (%)
  
  notifications:
    slack:
      enabled: false
      webhook_url: ""
      channel: "#alerts"
    
    email:
      enabled: false
      smtp_host: ""
      smtp_port: 587
      username: ""
      password: ""
      to: []
    
    webhook:
      enabled: false
      url: ""
```

### Cost Management Configuration

```yaml
cost:
  update_interval: 5m     # Cost data update interval
  providers: ["aws"]      # Enabled cloud providers
  
  aws:
    region: ""
    access_key_id: ""
    secret_access_key: ""
  
  gcp:
    project_id: ""
    service_account_path: ""
  
  azure:
    subscription_id: ""
    client_id: ""
    client_secret: ""
    tenant_id: ""
```

### Optimization Configuration

```yaml
optimization:
  auto_scale: false                 # Enable automatic scaling
  bin_packing: true                 # Enable bin packing
  spot_recommendations: true        # Enable spot recommendations
  recommendation_interval: 10m     # Recommendation generation interval
```

## Environment Variables

The following environment variables can override configuration:

- `LOG_LEVEL`: Set logging level (debug, info, warn, error)
- `KUBECONFIG`: Path to kubeconfig file
- `AWS_ACCESS_KEY_ID`: AWS access key
- `AWS_SECRET_ACCESS_KEY`: AWS secret key
- `GCP_PROJECT_ID`: GCP project ID
- `AZURE_SUBSCRIPTION_ID`: Azure subscription ID

## Configuration Validation

KubeCostGuard validates configuration on startup and will fail with descriptive errors if:

- Required cloud provider credentials are missing
- Invalid time durations are specified
- Invalid threshold values are provided
- Required Kubernetes permissions are not available
