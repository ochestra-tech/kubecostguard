#!/bin/bash

set -e

# Deployment script for KubeCostGuard

NAMESPACE=${NAMESPACE:-kubecostguard}
DEPLOYMENT_METHOD=${DEPLOYMENT_METHOD:-helm}
VERSION=${VERSION:-latest}

echo "Deploying KubeCostGuard using ${DEPLOYMENT_METHOD}"

case $DEPLOYMENT_METHOD in
  "helm")
    echo "Deploying with Helm..."
    helm upgrade --install kubecostguard deployments/helm/ \
      --namespace ${NAMESPACE} \
      --create-namespace \
      --set image.tag=${VERSION}
    ;;
  
  "kustomize")
    echo "Deploying with Kustomize..."
    kubectl apply -k deployments/kustomize/
    ;;
  
  *)
    echo "Unknown deployment method: ${DEPLOYMENT_METHOD}"
    echo "Available methods: helm, kustomize"
    exit 1
    ;;
esac

echo "Deployment completed!"
echo "Check status with: kubectl get pods -n ${NAMESPACE}"