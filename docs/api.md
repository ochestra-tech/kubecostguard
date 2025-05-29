# API Reference

## Base URL

All API endpoints are relative to `/api/v1/`

## Authentication

Currently, KubeCostGuard API does not require authentication. This is planned for future versions.

## Endpoints

### Health Endpoints

#### GET /health
Application health check.

**Response:**
```json
{
  "status": "healthy",
  "timestamp": "2024-01-15T10:30:00Z"
}
```

#### GET /ready
Readiness check.

**Response:**
```json
{
  "status": "ready"
}
```

### Cluster Health

#### GET /health-monitoring/cluster
Get overall cluster health.

**Response:**
```json
{
  "node": [...],
  "pod": [...],
  "controlplane": {...}
}
```

#### GET /health-monitoring/nodes
Get node health information.

**Response:**
```json
[
  {
    "name": "node-1",
    "ready": true,
    "conditions": [...],
    "capacity": {...},
    "allocatable": {...}
  }
]
```

#### GET /health-monitoring/pods
Get pod health information.

**Query Parameters:**
- `namespace` (optional): Filter by namespace

**Response:**
```json
[
  {
    "name": "pod-1",
    "namespace": "default",
    "phase": "Running",
    "conditions": [...],
    "restarts": 0
  }
]
```

#### GET /health-monitoring/alerts
Get active alerts.

**Response:**
```json
[
  {
    "type": "NodeNotReady",
    "severity": "critical",
    "message": "Node node-1 is not ready",
    "resource": "node-1",
    "timestamp": "2024-01-15T10:30:00Z"
  }
]
```

### Cost Management

#### GET /cost/analysis
Get cost analysis data.

**Query Parameters:**
- `days` (optional): Number of days to analyze (default: 7)

**Response:**
```json
{
  "period_days": 7,
  "total_cost": 1250.50,
  "breakdown": {
    "by_provider": {...},
    "by_service": {...},
    "by_namespace": {...}
  }
}
```

#### GET /cost/recommendations
Get cost optimization recommendations.

**Response:**
```json
{
  "recommendations": [...],
  "potential_savings": 125.75
}
```

#### GET /cost/providers
Get enabled cost providers.

**Response:**
```json
{
  "providers": ["aws", "gcp"]
}
```

#### POST /cost/update
Trigger cost data update.

**Response:**
```json
{
  "status": "update triggered"
}
```

### Optimization

#### GET /optimization/recommendations
Get optimization recommendations.

**Response:**
```json
{
  "recommendations": [
    {
      "type": "rightsizing",
      "resource": "deployment/nginx",
      "namespace": "default",
      "description": "Reduce CPU request",
      "potential_savings": 25.50
    }
  ]
}
```

#### POST /optimization/apply
Apply optimization recommendations.

**Request Body:**
```json
{
  "recommendation_ids": ["rec-123", "rec-456"],
  "dry_run": false
}
```

**Response:**
```json
{
  "status": "applied",
  "dry_run": false
}
```

#### GET /optimization/bin-packing
Get bin packing recommendations.

**Response:**
```json
{
  "recommendations": [...]
}
```

#### GET /optimization/spot-instances
Get spot instance recommendations.

**Response:**
```json
{
  "recommendations": [...]
}
```

### Metrics

#### GET /metrics
Prometheus metrics endpoint.

**Response:** Prometheus format metrics