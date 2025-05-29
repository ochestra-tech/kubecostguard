# Deployment Guide

## Prerequisites

- Kubernetes cluster (v1.20+)
- kubectl configured
- Helm 3.x (for Helm deployment)
- Docker (for building custom images)

## Deployment Methods

### 1. Helm Deployment (Recommended)

#### Quick Start
```bash
# Add the repository (when available)
helm repo add kubecostguard https://charts.kubecostguard.io
helm repo update

# Install with default values
helm install kubecostguard kubecostguard/kubecostguard
```

#### Custom Configuration
```bash
# Create custom values file
cat > values-custom.yaml << EOF
config:
  cost:
    providers: ["aws"]
    aws:
      region: "us-west-2"
      accessKeyId: "your-access-key"
      secretAccessKey: "your-secret-key"
  
  health:
    notifications:
      slack:
        enabled: true
        webhookUrl: "https://hooks.slack.com/services/YOUR/SLACK/WEBHOOK"
        channel: "#alerts"

resources:
  requests:
    cpu: 500m
    memory: 512Mi
  limits:
    cpu: 1000m
    memory: 1Gi
EOF

# Install with custom values
helm install kubecostguard kubecostguard/kubecostguard -f values-custom.yaml
```

#### Upgrade
```bash
helm upgrade kubecostguard kubecostguard/kubecostguard -f values-custom.yaml
```

### 2. Kustomize Deployment

```bash
# Deploy using kustomize
kubectl apply -k deployments/kustomize/

# Check deployment status
kubectl get pods -n kubecostguard
```

### 3. Manual Deployment

```bash
# Create namespace
kubectl create namespace kubecostguard

# Apply manifests
kubectl apply -f deployments/kustomize/serviceaccount.yaml
kubectl apply -f deployments/kustomize/rbac.yaml
kubectl apply -f deployments/kustomize/configmap.yaml
kubectl apply -f deployments/kustomize/deployment.yaml
kubectl apply -f deployments/kustomize/service.yaml
```

## Configuration

### Cloud Provider Setup

#### AWS
1. Create IAM user with required permissions:
   - Cost Explorer read access
   - EC2 read access
   - CloudWatch read access

2. Create Kubernetes secret:
```bash
kubectl create secret generic kubecostguard-aws \
  --from-literal=access-key-id=YOUR_ACCESS_KEY \
  --from-literal=secret-access-key=YOUR_SECRET_KEY \
  -n kubecostguard
```

#### GCP
1. Create service account with required roles:
   - Cloud Asset Viewer
   - Compute Viewer
   - Cloud Billing Account Viewer

2. Create Kubernetes secret:
```bash
kubectl create secret generic kubecostguard-gcp \
  --from-file=service-account.json=path/to/service-account.json \
  -n kubecostguard
```

#### Azure
1. Create service principal with required permissions:
   - Cost Management Reader
   - Reader (for compute resources)

2. Create Kubernetes secret:
```bash
kubectl create secret generic kubecostguard-azure \
  --from-literal=subscription-id=YOUR_SUBSCRIPTION_ID \
  --from-literal=client-id=YOUR_CLIENT_ID \
  --from-literal=client-secret=YOUR_CLIENT_SECRET \
  --from-literal=tenant-id=YOUR_TENANT_ID \
  -n kubecostguard
```

### Ingress Setup

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: kubecostguard
  namespace: kubecostguard
  annotations:
    kubernetes.io/ingress.class: nginx
    cert-manager.io/cluster-issuer: letsencrypt-prod
spec:
  tls:
  - hosts:
    - kubecostguard.yourdomain.com
    secretName: kubecostguard-tls
  rules:
  - host: kubecostguard.yourdomain.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: kubecostguard
            port:
              number: 8080
```

## Monitoring Setup

### Prometheus Integration

KubeCostGuard exposes metrics at `/api/v1/metrics`. Configure Prometheus to scrape these metrics:

```yaml
# prometheus-config.yaml
- job_name: 'kubecostguard'
  static_configs:
  - targets: ['kubecostguard.kubecostguard.svc.cluster.local:8080']
  metrics_path: /api/v1/metrics
```

### Grafana Dashboard

Import the provided Grafana dashboard from `docs/grafana/kubecostguard-dashboard.json`.

## Scaling

### Horizontal Scaling
```bash
# Scale to 3 replicas
kubectl scale deployment kubecostguard --replicas=3 -n kubecostguard
```

### Vertical Scaling
Update resource requests/limits in your values file and upgrade:

```yaml
resources:
  requests:
    cpu: 1000m
    memory: 1Gi
  limits:
    cpu: 2000m
    memory: 2Gi
```

## Troubleshooting

### Common Issues

1. **Pod not starting**
   ```bash
   kubectl describe pod -l app=kubecostguard -n kubecostguard
   kubectl logs -l app=kubecostguard -n kubecostguard
   ```

2. **RBAC permissions**
   ```bash
   kubectl auth can-i get nodes --as=system:serviceaccount:kubecostguard:kubecostguard
   ```

3. **Cloud provider authentication**
   ```bash
   kubectl logs -l app=kubecostguard -n kubecostguard | grep -i "auth\|credential\|permission"
   ```

### Health Checks

```bash
# Check pod health
kubectl get pods -n kubecostguard

# Check service
kubectl get svc -n kubecostguard

# Test API endpoint
kubectl port-forward svc/kubecostguard 8080:8080 -n kubecostguard
curl http://localhost:8080/api/v1/health
```